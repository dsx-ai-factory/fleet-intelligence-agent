// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package healthserver provides a simplified HTTP server for Fleet Intelligence metrics export.
// This server focuses only on health monitoring and metrics export, removing all
// management functionality like package management, control plane connectivity,
// fault injection, and plugin systems.
package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	stdos "os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	pkgfaultinjector "github.com/NVIDIA/fleet-intelligence-sdk/pkg/fault-injector"
	pkghost "github.com/NVIDIA/fleet-intelligence-sdk/pkg/host"
	pkgkmsgwriter "github.com/NVIDIA/fleet-intelligence-sdk/pkg/kmsg/writer"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	pkgmetricsrecorder "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics/recorder"
	pkgmetricsscraper "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics/scraper"
	pkgmetricsstore "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics/store"
	pkgmetricssyncer "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics/syncer"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/agentstate"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/attestation"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/inventory"
	inventorysink "github.com/dsx-ai-factory/fleet-intelligence-agent/internal/inventory/sink"
	inventorysource "github.com/dsx-ai-factory/fleet-intelligence-agent/internal/inventory/source"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/machineinfo"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/nodeidentity"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/registry"
)

// Server is a simplified health metrics exporter server
type Server struct {
	auditLogger log.AuditLogger
	dbRW        *sql.DB
	dbRO        *sql.DB

	componentsRegistry components.Registry
	gpudInstance       *components.GPUdInstance

	config *config.Config

	// healthExporter is the health exporter instance
	healthExporter exporter.Exporter

	// faultInjector is the fault injector for testing
	faultInjector pkgfaultinjector.Injector

	// srv and listener are stored so Stop() can perform a graceful shutdown.
	srv      *http.Server
	listener net.Listener

	// stopOnce ensures Stop() is idempotent. The defer in startServer and the
	// signal handler in run.go both call Stop(), so without this guard
	// components, databases, and the health exporter would be closed twice.
	stopOnce sync.Once
	loopWG   sync.WaitGroup

	// loopCtx/loopCancel control background inventory and attestation goroutines.
	// Stop() cancels this context and waits on loopWG for graceful shutdown.
	loopCtx    context.Context
	loopCancel context.CancelFunc

	machineID string
}

type inventoryMachineInfoCollectorFunc func(context.Context) (*machineinfo.MachineInfo, error)

func (f inventoryMachineInfoCollectorFunc) Collect(ctx context.Context) (*machineinfo.MachineInfo, error) {
	return f(ctx)
}

// initializeDatabases opens and initializes database connections
func initializeDatabases(ctx context.Context, cfg *config.Config) (*sql.DB, *sql.DB, error) {
	stateFile := ":memory:"
	if cfg.State != "" {
		stateFile = cfg.State
	}

	dbRW, err := sqlite.Open(stateFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open state file (for read-write): %w", err)
	}

	dbRO, err := sqlite.Open(stateFile, sqlite.WithReadOnly(true))
	if err != nil {
		dbRW.Close()
		return nil, nil, fmt.Errorf("failed to open state file (for read-only): %w", err)
	}

	if err := pkgmetadata.CreateTableMetadata(ctx, dbRW); err != nil {
		dbRO.Close()
		dbRW.Close()
		return nil, nil, fmt.Errorf("failed to create metadata table: %w", err)
	}
	if err := config.SecureStateFilePermissions(stateFile); err != nil {
		dbRO.Close()
		dbRW.Close()
		return nil, nil, fmt.Errorf("failed to secure state file permissions: %w", err)
	}

	return dbRW, dbRO, nil
}

type dbNodeUUIDState struct {
	dbRW *sql.DB
	dbRO *sql.DB
}

func (s dbNodeUUIDState) GetOrCreateNodeUUID(ctx context.Context, create func() (string, error)) (string, bool, error) {
	if err := pkgmetadata.CreateTableMetadata(ctx, s.dbRW); err != nil {
		return "", false, fmt.Errorf("create metadata table: %w", err)
	}
	return agentstate.GetOrCreateNodeUUIDMetadata(ctx, s.dbRW, create)
}

// initializeMachineID retrieves or creates a machine ID
// This establishes the agent's stable identity for metrics reporting.
// Priority: DB (persisted) → DMI product UUID → random UUID
func initializeMachineID(ctx context.Context, dbRW, dbRO *sql.DB) (string, error) {
	return nodeidentity.EnsureNodeUUID(ctx, dbNodeUUIDState{dbRW: dbRW, dbRO: dbRO})
}

// getHealthCheckInterval determines the health check interval from config
func getHealthCheckInterval(config *config.Config) time.Duration {
	return config.HealthCheckInterval()
}

func getInventorySyncInterval(config *config.Config) time.Duration {
	if config == nil {
		return 0
	}
	if config.Inventory != nil {
		if !config.Inventory.Enabled {
			return 0
		}
		return config.Inventory.Interval.Duration
	}
	return 0
}

func getInventorySyncTimeout(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Inventory == nil || !cfg.Inventory.Enabled {
		return 0
	}
	return config.DefaultInventoryTimeout
}

func getAttestationInterval(config *config.Config) time.Duration {
	if config == nil || config.Attestation == nil || !config.Attestation.Enabled {
		return 0
	}
	return config.Attestation.Interval.Duration
}

func getAttestationTimeout(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Attestation == nil || !cfg.Attestation.Enabled {
		return 0
	}
	return config.DefaultAttestationTimeout
}

func waitForWaitGroup(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	if timeout <= 0 {
		<-done
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// shouldEnableComponent determines if a component should be enabled based on configuration
func shouldEnableComponent(name string, enabledByDefault bool, config *config.Config) bool {
	shouldEnable := enabledByDefault

	// If specific components are configured, check if this one is selected
	if len(config.Components) > 0 && config.Components[0] != "*" && config.Components[0] != "all" {
		shouldEnable = config.ShouldEnable(name)
	}

	// Explicit disable takes precedence
	if config.ShouldDisable(name) {
		shouldEnable = false
	}

	return shouldEnable
}

// New creates a new simplified health server for metrics export only
func New(ctx context.Context, auditLogger log.AuditLogger, config *config.Config) (retServer *Server, retErr error) {
	// Initialize database connections
	dbRW, dbRO, err := initializeDatabases(ctx, config)
	if err != nil {
		return nil, err
	}

	loopCtx, loopCancel := context.WithCancel(ctx)
	s := &Server{
		auditLogger: auditLogger,
		dbRW:        dbRW,
		dbRO:        dbRO,
		config:      config,
		loopCtx:     loopCtx,
		loopCancel:  loopCancel,
	}
	defer func() {
		if retErr != nil {
			s.Stop()
		}
	}()

	// Initialize machine ID
	machineID, err := initializeMachineID(ctx, dbRW, dbRO)
	if err != nil {
		return nil, err
	}
	s.machineID = machineID

	// Initialize fault injector for testing (only if enabled)
	if config.EnableFaultInjection {
		log.Logger.Infow("fault injection enabled for testing")
		kmsgWriter := pkgkmsgwriter.NewWriter(pkgkmsgwriter.DefaultDevKmsg)
		s.faultInjector = pkgfaultinjector.NewInjector(kmsgWriter)
	} else {
		log.Logger.Infow("fault injection disabled")
		s.faultInjector = nil
	}

	dcgmGroupNames := components.NewDCGMGroupNames(fmt.Sprintf("daemon-%d", stdos.Getpid()))

	// Initialize DCGM instance
	dcgmInitCtx, dcgmInitCancel := context.WithTimeout(ctx, time.Minute)
	dcgmInstance, err := nvidiadcgm.NewWithContextAndGroupName(dcgmInitCtx, dcgmGroupNames.HealthMonitoringGroup)
	dcgmInitCancel()
	if err != nil {
		return nil, fmt.Errorf("failed to create DCGM instance: %w", err)
	}
	// Make the instance reachable by Stop immediately so failures later in
	// initialization do not leak DCGM resources.
	s.gpudInstance = &components.GPUdInstance{DCGMInstance: dcgmInstance}

	// Create event store needed for health exporter
	log.Logger.Infow("initializing event store", "retention", config.RetentionPeriod.Duration)
	eventStore, err := eventstore.New(dbRW, dbRO, config.RetentionPeriod.Duration)
	if err != nil {
		return nil, fmt.Errorf("failed to open events database: %w", err)
	}

	// Create reboot event store and record reboot
	rebootEventStore := pkghost.NewRebootEventStore(eventStore)
	cctx, ccancel := context.WithTimeout(ctx, time.Minute)
	err = rebootEventStore.RecordReboot(cctx)
	ccancel()
	if err != nil {
		log.Logger.Errorw("failed to record reboot", "error", err)
	}

	// Determine health check interval
	healthCheckInterval := getHealthCheckInterval(config)

	// Create shared DCGM caches
	dcgmHealthCache := nvidiadcgm.NewHealthCache(ctx, dcgmInstance, healthCheckInterval)
	log.Logger.Infow("DCGM health check cache configured", "healthCheckInterval", healthCheckInterval)

	dcgmFieldValueCache := nvidiadcgm.NewFieldValueCache(ctx, dcgmInstance, healthCheckInterval)
	log.Logger.Infow("DCGM field value cache created", "healthCheckInterval", healthCheckInterval)
	s.gpudInstance = &components.GPUdInstance{
		RootCtx:              ctx,
		MachineID:            machineID,
		DCGMInstance:         dcgmInstance,
		DCGMHealthCache:      dcgmHealthCache,
		DCGMFieldValueCache:  dcgmFieldValueCache,
		DCGMGroupNames:       dcgmGroupNames,
		NVIDIAToolOverwrites: config.NvidiaToolOverwrites,
		DBRW:                 dbRW,
		DBRO:                 dbRO,
		EventStore:           eventStore,
		RebootEventStore:     rebootEventStore,
		MountPoints:          []string{"/"},
		MountTargets:         []string{},
		HealthCheckInterval:  healthCheckInterval,
	}

	// Register only enabled components for health monitoring
	s.componentsRegistry = components.NewRegistry(s.gpudInstance)
	for _, c := range registry.All() {
		if shouldEnableComponent(c.Name, c.EnabledByDefault, config) {
			s.componentsRegistry.MustRegister(c.InitFunc)
		}
	}

	// Start DCGM health cache before starting components
	if dcgmHealthCache != nil {
		if err := dcgmHealthCache.Start(); err != nil {
			log.Logger.Errorw("failed to start DCGM health cache, DCGM health monitoring disabled", "error", err)
		}
	}

	// Set up DCGM field watching after all components have registered their fields
	if dcgmFieldValueCache != nil {
		if err := dcgmFieldValueCache.SetupFieldWatchingWithName(dcgmGroupNames.GPUFieldGroup); err != nil {
			log.Logger.Errorw("failed to set up DCGM field watching, DCGM metrics collection unavailable", "error", err)
		}
	}

	// Start DCGM field value cache polling
	if dcgmFieldValueCache != nil {
		if err := dcgmFieldValueCache.Start(); err != nil {
			log.Logger.Errorw("failed to start DCGM field value cache, DCGM metrics polling disabled", "error", err)
		}
	}

	// Start components for health monitoring (must be started after DCGM initialization)
	for _, c := range s.componentsRegistry.All() {
		if err = c.Start(); err != nil {
			return nil, fmt.Errorf("failed to start component %s: %w", c.Name(), err)
		}
	}

	// Create metrics infrastructure needed for health exporter
	promScraper, err := pkgmetricsscraper.NewPrometheusScraper(pkgmetrics.DefaultGatherer())
	if err != nil {
		return nil, fmt.Errorf("failed to create scraper: %w", err)
	}
	metricsSQLiteStore, err := pkgmetricsstore.NewSQLiteStore(ctx, dbRW, dbRO, pkgmetricsstore.DefaultTableName)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics store: %w", err)
	}
	// Purge metrics every 5 minutes (reasonable interval to balance overhead and timely cleanup)
	metricsPurgeInterval := 5 * time.Minute
	log.Logger.Infow("initializing metrics syncer", "scrapeInterval", healthCheckInterval, "purgeInterval", metricsPurgeInterval, "retention", config.RetentionPeriod.Duration)
	syncer := pkgmetricssyncer.NewSyncer(ctx, promScraper, metricsSQLiteStore, healthCheckInterval, metricsPurgeInterval, config.RetentionPeriod.Duration)
	syncer.Start()

	promRecorder := pkgmetricsrecorder.NewPrometheusRecorder(ctx, 15*time.Minute, dbRO)
	promRecorder.Start()

	s.startInventoryLoop(loopCtx, config, dcgmInstance)
	s.startAttestationLoop(loopCtx, config)

	// Create and start health exporter with all dependencies if enabled
	if config.HealthExporter != nil {
		var err error
		s.healthExporter, err = exporter.New(
			ctx,
			exporter.WithConfig(config.HealthExporter),
			exporter.WithMetricsStore(metricsSQLiteStore),
			exporter.WithEventStore(eventStore),
			exporter.WithComponentsRegistry(s.componentsRegistry),
			exporter.WithDCGM(dcgmInstance),
			exporter.WithDatabaseConnections(dbRW, dbRO),
			exporter.WithMachineID(machineID),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create health exporter: %w", err)
		}

		// Start the health exporter
		if err := s.healthExporter.Start(); err != nil {
			log.Logger.Errorw("failed to start health exporter", "error", err)
		}
	}

	// Start the HTTP server
	go s.startServer(ctx)

	return s, nil
}

func (s *Server) startInventoryLoop(
	ctx context.Context,
	cfg *config.Config,
	dcgmInstance nvidiadcgm.Instance,
) {
	interval := getInventorySyncInterval(cfg)
	if interval <= 0 {
		log.Logger.Infow("inventory loop disabled, skipping")
		return
	}
	timeout := getInventorySyncTimeout(cfg)
	log.Logger.Infow("inventory loop starting",
		"interval", interval,
		"retry_interval", inventory.DefaultRetryInterval,
		"timeout", timeout,
		"startup_jitter", inventory.DefaultStartupJitter)

	allComponents := registry.AllComponentNames()
	retentionPeriodSeconds, enabledComponents, disabledComponents := cfg.InventoryAgentConfig(allComponents)
	inventoryEnabled, inventoryIntervalSeconds := cfg.InventoryLoopAgentConfig()
	attestationEnabled, attestationIntervalSeconds := cfg.AttestationLoopAgentConfig()

	source := inventorysource.NewMachineInfoSourceWithAgentConfig(
		inventoryMachineInfoCollectorFunc(func(context.Context) (*machineinfo.MachineInfo, error) {
			return machineinfo.GetMachineInfo(dcgmInstance.GetDevices())
		}),
		&inventory.AgentConfig{
			TotalComponents:             int64(len(allComponents)),
			RetentionPeriodSeconds:      retentionPeriodSeconds,
			MetricScrapeIntervalSeconds: cfg.MetricScrapeIntervalSeconds(),
			EnabledComponents:           enabledComponents,
			DisabledComponents:          disabledComponents,
			InventoryEnabled:            inventoryEnabled,
			InventoryIntervalSeconds:    inventoryIntervalSeconds,
			AttestationEnabled:          attestationEnabled,
			AttestationIntervalSeconds:  attestationIntervalSeconds,
		},
	)
	sink := inventorysink.NewBackendSink(agentstate.NewSQLite())
	manager := inventory.NewManager(source, sink, inventory.InventoryConfig{
		Interval:      interval,
		RetryInterval: inventory.DefaultRetryInterval,
		Timeout:       timeout,
		StartupJitter: inventory.DefaultStartupJitter,
	})

	s.loopWG.Add(1)
	go func() {
		defer s.loopWG.Done()
		if err := manager.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Logger.Errorw("inventory loop manager exited", "error", err)
		}
	}()
}

func (s *Server) startAttestationLoop(ctx context.Context, cfg *config.Config) {
	interval := getAttestationInterval(cfg)
	if interval <= 0 {
		log.Logger.Infow("attestation loop disabled, skipping")
		return
	}
	timeout := getAttestationTimeout(cfg)
	log.Logger.Infow("attestation loop starting",
		"interval", interval,
		"retry_interval", attestation.DefaultRetryInterval,
		"timeout", timeout,
		"startup_jitter", attestation.DefaultStartupJitter)

	state := agentstate.NewSQLite()
	manager := attestation.NewManager(
		attestation.NewStateNodeUUIDProvider(state),
		attestation.NewStateJWTProvider(state),
		attestation.NewStateNonceProvider(state),
		attestation.NewCLIEvidenceCollector(timeout),
		attestation.NewStateBackendSubmitter(state),
		attestation.AttestationConfig{
			Interval:      interval,
			RetryInterval: attestation.DefaultRetryInterval,
			Timeout:       timeout,
			StartupJitter: attestation.DefaultStartupJitter,
		},
	)

	s.loopWG.Add(1)
	go func() {
		defer s.loopWG.Done()
		if err := manager.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Logger.Errorw("attestation loop exited", "error", err)
		}
	}()
}

// GetHealthExporter returns the health exporter instance (for offline mode access)
func (s *Server) GetHealthExporter() exporter.Exporter {
	return s.healthExporter
}

// Stop gracefully stops the server. It shuts down the HTTP listener first
// (draining in-flight requests), then tears down components and databases,
// and finally removes the unix socket file if one was used.
// Stop is safe to call multiple times; the defer in startServer and the
// signal handler in run.go both invoke it.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		// Signal inventory/attestation loops to stop and wait for graceful exit
		// before we close dependencies they may still be using.
		if s.loopCancel != nil {
			s.loopCancel()
		}
		if !waitForWaitGroup(&s.loopWG, 10*time.Second) {
			log.Logger.Warnw("timed out waiting for background loops to stop")
		}

		// Gracefully shut down the HTTP server so in-flight requests complete
		// before we close databases and components underneath them.
		if s.srv != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.srv.Shutdown(shutdownCtx); err != nil {
				log.Logger.Warnw("HTTP server shutdown error, forcing close", "error", err)
				_ = s.srv.Close()
			}
		}
		if s.listener != nil {
			// Go's UnixListener.Close() automatically unlinks the socket file.
			_ = s.listener.Close()
		}

		// Stop health exporter if running
		if s.healthExporter != nil {
			if err := s.healthExporter.Stop(); err != nil {
				log.Logger.Errorw("failed to stop health exporter", "error", err)
			}
		}

		// Stop DCGM health cache polling to prevent goroutine leak
		if s.gpudInstance != nil && s.gpudInstance.DCGMHealthCache != nil {
			s.gpudInstance.DCGMHealthCache.Stop()
			log.Logger.Debugw("stopped DCGM health cache")
		}

		// Stop DCGM field value cache polling to prevent goroutine leak
		if s.gpudInstance != nil && s.gpudInstance.DCGMFieldValueCache != nil {
			s.gpudInstance.DCGMFieldValueCache.Stop()
			log.Logger.Debugw("stopped DCGM field value cache")
		}

		if s.componentsRegistry != nil {
			for _, component := range s.componentsRegistry.All() {
				if err := component.Close(); err != nil {
					log.Logger.Errorf("failed to close plugin %v: %v", component.Name(), err)
				}
			}
		}

		if s.gpudInstance != nil && s.gpudInstance.DCGMInstance != nil {
			if err := s.gpudInstance.DCGMInstance.Shutdown(); err != nil {
				log.Logger.Warnw("failed to shutdown DCGM instance", "error", err)
			}
		}

		if s.dbRW != nil {
			if cerr := s.dbRW.Close(); cerr != nil {
				log.Logger.Debugw("failed to close read-write db", "error", cerr)
			} else {
				log.Logger.Debugw("successfully closed read-write db")
			}
		}
		if s.dbRO != nil {
			if cerr := s.dbRO.Close(); cerr != nil {
				log.Logger.Debugw("failed to close read-only db", "error", cerr)
			} else {
				log.Logger.Debugw("successfully closed read-only db")
			}
		}
	})
}

// removeStaleSocket removes path only if it is an existing unix socket.
// It refuses to delete regular files, directories, or symlinks so that a
// misconfigured --listen-address cannot cause data loss.
func removeStaleSocket(path string) error {
	info, err := stdos.Lstat(path)
	if err != nil {
		if stdos.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&stdos.ModeSymlink != 0 {
		return fmt.Errorf("%q is a symlink, not a socket", path)
	}
	if info.Mode()&stdos.ModeSocket == 0 {
		return fmt.Errorf("%q exists but is not a unix socket", path)
	}
	return stdos.Remove(path)
}

// startServer creates and starts the HTTP server
func (s *Server) startServer(ctx context.Context) {
	defer s.Stop()

	// Create metrics store for health data
	metricsSQLiteStore, err := pkgmetricsstore.NewSQLiteStore(ctx, s.dbRW, s.dbRO, pkgmetricsstore.DefaultTableName)
	if err != nil {
		log.Logger.Errorw("failed to create metrics store", "error", err)
		return
	}

	router := gin.Default()
	s.installMiddlewares(router)

	globalHandler := newGlobalHandler(s.config, s.componentsRegistry, metricsSQLiteStore, s.gpudInstance)

	v1Group := router.Group("/v1")
	v1Group.Use(gzip.Gzip(gzip.DefaultCompression))
	v1Group.GET("/states", globalHandler.getHealthStates)
	v1Group.GET("/events", globalHandler.getEvents)
	v1Group.GET("/info", globalHandler.getInfo)
	v1Group.GET("/metrics", globalHandler.getMetrics)

	// Core endpoints for health monitoring
	promHandler := promhttp.HandlerFor(pkgmetrics.DefaultGatherer(), promhttp.HandlerOpts{})
	router.GET("/metrics", func(ctx *gin.Context) {
		promHandler.ServeHTTP(ctx.Writer, ctx.Request)
	})
	router.GET("/healthz", s.healthz())
	router.GET("/machine-info", globalHandler.machineInfo)

	// Only register fault injection endpoint if explicitly enabled
	if s.config.EnableFaultInjection {
		log.Logger.Infow("registering fault injection endpoint", "path", URLPathInjectFault)
		router.POST(URLPathInjectFault, s.injectFault)
	} else {
		log.Logger.Debugw("fault injection endpoint disabled")
	}

	s.srv = &http.Server{
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if strings.HasPrefix(s.config.Address, "/") {
		socketPath := s.config.Address
		// Probe the existing socket: if a daemon is already listening, refuse
		// to start rather than stealing its socket path.
		if conn, err := net.DialTimeout("unix", socketPath, 2*time.Second); err == nil {
			_ = conn.Close()
			log.Logger.Errorw("another instance is already listening on this socket", "path", socketPath)
			stdos.Exit(1)
		}
		// Remove a stale socket left by a previous (dead) run.
		if err := removeStaleSocket(socketPath); err != nil {
			log.Logger.Errorw("refusing to overwrite non-socket file", "path", socketPath, "error", err)
			stdos.Exit(1)
		}
		if err := stdos.MkdirAll(filepath.Dir(socketPath), 0o750); err != nil {
			log.Logger.Errorw("failed to create socket directory", "path", socketPath, "error", err)
			stdos.Exit(1)
		}
		var err error
		s.listener, err = net.Listen("unix", socketPath)
		if err != nil {
			log.Logger.Errorw("failed to listen on unix socket", "path", socketPath, "error", err)
			stdos.Exit(1)
		}
		// Restrict the socket to owner only; only root (or a group member if chgrp'd) can connect.
		if err := stdos.Chmod(socketPath, 0o600); err != nil {
			_ = s.listener.Close()
			log.Logger.Errorw("failed to set socket permissions", "path", socketPath, "error", err)
			stdos.Exit(1)
		}
		log.Logger.Infow("fleetint started serving with Unix socket", "path", socketPath)
		if err := s.srv.Serve(s.listener); err != nil && err != http.ErrServerClosed {
			log.Logger.Warnw("fleetint serve failed", "path", socketPath, "error", err)
			stdos.Exit(1)
		}
		return
	}

	log.Logger.Infow("fleetint started serving with HTTP", "address", s.config.Address)
	s.srv.Addr = s.config.Address
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Logger.Warnw("fleetint serve failed", "address", s.config.Address, "error", err)
		stdos.Exit(1)
	}
}

// installMiddlewares installs basic middleware for the router
func (s *Server) installMiddlewares(router *gin.Engine) {
	router.Use(gin.Recovery())
	router.Use(securityHeaders())
}

// securityHeaders returns middleware that sets standard security response headers.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// healthz returns a simple health check handler
func (s *Server) healthz() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": "v1",
		})
	}
}
