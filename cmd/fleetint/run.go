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

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	nvidiainfiniband "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/infiniband"
	infinibandtypes "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/infiniband/types"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	"github.com/gin-gonic/gin"
	"github.com/urfave/cli"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/config"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/server"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/version"
)

// restrictedOfflinePaths lists directories into which the agent must never write output.
// These are system-owned paths where writing as root could corrupt the OS.
// /var is intentionally not blocked wholesale because subdirectories like
// /var/data or /var/opt are legitimate output locations for customers.
var restrictedOfflinePaths = []string{
	"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64",
	"/proc", "/run", "/sbin", "/sys", "/usr",
}

// validateOfflinePath ensures the --path flag points to a safe, absolute directory.
// It resolves symlinks in existing parent components so that a symlink pointing
// into a restricted directory is caught before any files are written.
func validateOfflinePath(p string) error {
	if p == "" {
		return fmt.Errorf("--path must not be empty when --offline-mode is set")
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("--path must be an absolute path, got %q", p)
	}
	clean := filepath.Clean(p)

	// Reject if the path itself is a symlink.
	if info, err := os.Lstat(clean); err == nil && (info.Mode()&os.ModeSymlink) != 0 {
		return fmt.Errorf("--path %q is a symlink", p)
	}

	// Resolve the deepest existing ancestor so symlinked parents are caught.
	resolved := clean
	cur := clean
	for {
		rp, err := filepath.EvalSymlinks(cur)
		if err == nil {
			resolved = filepath.Clean(filepath.Join(rp, strings.TrimPrefix(clean, cur)))
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to resolve path %q: %w", cur, err)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	for _, r := range restrictedOfflinePaths {
		if resolved == r || strings.HasPrefix(resolved, r+"/") {
			return fmt.Errorf("--path %q resolves to %q which is inside restricted system directory %q", p, resolved, r)
		}
	}
	return nil
}

// parseDuration parses duration in HH:MM:SS format
func parseDuration(durationStr string) (time.Duration, error) {
	// Regular expression to match HH:MM:SS format
	re := regexp.MustCompile(`^(\d{1,2}):(\d{2}):(\d{2})$`)
	matches := re.FindStringSubmatch(durationStr)
	if matches == nil {
		return 0, fmt.Errorf("invalid duration format, expected HH:MM:SS")
	}

	hours, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("invalid hours: %v", err)
	}

	minutes, err := strconv.Atoi(matches[2])
	if err != nil {
		return 0, fmt.Errorf("invalid minutes: %v", err)
	}

	seconds, err := strconv.Atoi(matches[3])
	if err != nil {
		return 0, fmt.Errorf("invalid seconds: %v", err)
	}

	if minutes >= 60 || seconds >= 60 {
		return 0, fmt.Errorf("minutes and seconds must be less than 60")
	}

	totalDuration := time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second
	return totalDuration, nil
}

// Helper functions for environment variable configuration

func setBoolFromEnv(envKey string, target *bool, logMsg, logKey string) error {
	if val := os.Getenv(envKey); val != "" {
		b, err := strconv.ParseBool(val)
		if err != nil {
			return fmt.Errorf("invalid %s value: %v", envKey, val)
		}
		*target = b
		log.Logger.Infow(logMsg, logKey, b)
	}
	return nil
}

func setDurationFromEnv(envKey string, target *metav1.Duration, logMsg, logKey string, min, max time.Duration) error {
	if val := os.Getenv(envKey); val != "" {
		d, err := time.ParseDuration(val)
		if err != nil {
			return fmt.Errorf("invalid %s value: %v", envKey, val)
		}
		if min > 0 && d < min {
			return fmt.Errorf("%s must be at least %v, got %v", envKey, min, d)
		}
		if max > 0 && d > max {
			return fmt.Errorf("%s must be at most %v, got %v", envKey, max, d)
		}
		target.Duration = d
		log.Logger.Infow(logMsg, logKey, d)
	}
	return nil
}

func setIntFromEnv(envKey string, target *int, logMsg, logKey string, min int) error {
	if val := os.Getenv(envKey); val != "" {
		i, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid %s value: %v", envKey, val)
		}
		if i < min {
			return fmt.Errorf("%s must be at least %d, got %d", envKey, min, i)
		}
		*target = i
		log.Logger.Infow(logMsg, logKey, i)
	}
	return nil
}

// configureHealthExporterFromEnv reads environment variables and applies them to the health exporter configuration
func configureHealthExporterFromEnv(cfg *config.Config) error {
	// Ensure HealthExporter config exists
	if cfg.HealthExporter == nil {
		return fmt.Errorf("health exporter config is nil")
	}
	he := cfg.HealthExporter

	if err := setBoolFromEnv("FLEETINT_INCLUDE_METRICS", &he.IncludeMetrics, "set health exporter include metrics from env", "include_metrics"); err != nil {
		return err
	}

	if err := setBoolFromEnv("FLEETINT_INCLUDE_EVENTS", &he.IncludeEvents, "set health exporter include events from env", "include_events"); err != nil {
		return err
	}

	if err := setBoolFromEnv("FLEETINT_INCLUDE_MACHINEINFO", &he.IncludeMachineInfo, "set health exporter include machine info from env", "include_machine_info"); err != nil {
		return err
	}

	if err := setBoolFromEnv("FLEETINT_INCLUDE_HEALTHCHECKS", &he.IncludeComponentData, "set health exporter include component data from env", "include_component_data"); err != nil {
		return err
	}

	if err := setDurationFromEnv("FLEETINT_COLLECT_INTERVAL", &he.Interval, "set health exporter interval from env", "interval", time.Second, 24*time.Hour); err != nil {
		return err
	}

	// Lookbacks
	if err := setDurationFromEnv("FLEETINT_METRICS_LOOKBACK", &he.MetricsLookback, "set health exporter metrics lookback from env", "metrics_lookback", 0, 0); err != nil {
		return err
	}

	if err := setDurationFromEnv("FLEETINT_EVENTS_LOOKBACK", &he.EventsLookback, "set health exporter events lookback from env", "events_lookback", 0, 0); err != nil {
		return err
	}

	if err := setDurationFromEnv("FLEETINT_CHECK_INTERVAL", &he.HealthCheckInterval, "set health exporter health check interval from env", "health_check_interval", time.Second, 24*time.Hour); err != nil {
		return err
	}

	if err := setIntFromEnv("FLEETINT_RETRY_MAX_ATTEMPTS", &he.RetryMaxAttempts, "set health exporter retry max attempts from env", "retry_max_attempts", 0); err != nil {
		return err
	}

	// OTel gateway collector mode: when set, metrics/logs are routed to the gateway
	// instead of the backend directly. Enrollment remains independent for inventory
	// and attestation; backend credentials are not forwarded to the gateway.
	if val := os.Getenv("FLEETINT_COLLECTOR_ENDPOINT"); val != "" {
		he.CollectorEndpoint = val
		log.Logger.Infow("set OTel gateway collector endpoint from env", "collector_endpoint", val)
	}

	return nil
}

func configureLoopConfigFromEnv(cfg *config.Config) error {
	if cfg.Inventory != nil {
		if err := setBoolFromEnv("FLEETINT_INVENTORY_ENABLED", &cfg.Inventory.Enabled, "set inventory enabled from env", "inventory_enabled"); err != nil {
			return err
		}
		if err := setDurationFromEnv("FLEETINT_INVENTORY_INTERVAL", &cfg.Inventory.Interval, "set inventory interval from env", "inventory_interval", config.MinInventoryInterval, 0); err != nil {
			return err
		}
	}
	if cfg.Attestation != nil {
		if err := setBoolFromEnv("FLEETINT_ATTESTATION_ENABLED", &cfg.Attestation.Enabled, "set attestation enabled from env", "attestation_enabled"); err != nil {
			return err
		}
		if err := setDurationFromEnv("FLEETINT_ATTESTATION_INTERVAL", &cfg.Attestation.Interval, "set attestation interval from env", "attestation_interval", config.MinAttestationInterval, 0); err != nil {
			return err
		}
	}
	return nil
}

func runCommand(cliContext *cli.Context) error {
	logLevel := cliContext.String("log-level")
	logFile := cliContext.String("log-file")
	zapLvl, err := log.ParseLogLevel(logLevel)
	if err != nil {
		return err
	}
	log.Logger = log.CreateLogger(zapLvl, logFile)

	log.Logger.Debugw("starting run command")

	if runtime.GOOS != "linux" {
		fmt.Printf("fleetint run on %q not supported\n", runtime.GOOS)
		os.Exit(1)
	}

	if zapLvl.Level() > zap.DebugLevel { // e.g., info, warn, error
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.DebugMode)
	}

	listenAddress := cliContext.String("listen-address")
	retentionPeriod := cliContext.Duration("retention-period")

	ibClassRootDir := cliContext.String("infiniband-class-root-dir")
	components := cliContext.String("components")

	infinibandExpectedPortStates := cliContext.String("infiniband-expected-port-states")
	enableFaultInjection := cliContext.Bool("enable-fault-injection")

	// Fleet Intelligence Exporter configuration
	offlineMode := cliContext.Bool("offline-mode")
	offlineModePath := cliContext.String("path")
	offlineModeDurationStr := cliContext.String("duration")
	offlineModeOutputFormat := cliContext.String("format")

	var offlineModeDuration time.Duration

	if offlineMode {
		if offlineModePath == "" {
			return fmt.Errorf("--path is required when using --offline-mode")
		}
		if offlineModeDurationStr == "" {
			return fmt.Errorf("--duration is required when using --offline-mode")
		} else {
			parsedDuration, err := parseDuration(offlineModeDurationStr)
			if err != nil {
				return fmt.Errorf("invalid duration: %v", err)
			}
			offlineModeDuration = parsedDuration
		}
	}

	if len(infinibandExpectedPortStates) > 0 {
		var expectedPortStates infinibandtypes.ExpectedPortStates
		if err := json.Unmarshal([]byte(infinibandExpectedPortStates), &expectedPortStates); err != nil {
			return err
		}
		nvidiainfiniband.SetDefaultExpectedPortStates(expectedPortStates)

		log.Logger.Infow("set infiniband expected port states", "infinibandExpectedPortStates", infinibandExpectedPortStates)
	}

	configOpts := []config.OpOption{
		config.WithInfinibandClassRootDir(ibClassRootDir),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	cfg, err := config.Default(ctx, configOpts...)
	cancel()
	if err != nil {
		return err
	}

	// Apply environment variable overrides to health exporter configuration
	if err := configureHealthExporterFromEnv(cfg); err != nil {
		return fmt.Errorf("failed to configure health exporter from environment variables: %w", err)
	}
	if err := configureLoopConfigFromEnv(cfg); err != nil {
		return fmt.Errorf("failed to configure loop settings from environment variables: %w", err)
	}
	log.Logger.Infow("health exporter configuration", "cfg", cfg.HealthExporter)

	if listenAddress != "" {
		cfg.Address = listenAddress
	}

	if retentionPeriod > 0 {
		cfg.RetentionPeriod = metav1.Duration{Duration: retentionPeriod}
	}

	if components != "" {
		cfg.Components = strings.Split(components, ",")
	}

	// Only apply CLI flag if true, to avoid overwriting env var setting
	// (since we can't distinguish between explicit --enable-fault-injection=false and default false)
	if enableFaultInjection {
		cfg.EnableFaultInjection = true
	}

	if cfg.EnableFaultInjection {
		log.Logger.Infow("fault injection endpoint enabled for testing")
	} else {
		log.Logger.Infow("fault injection endpoint disabled")
	}

	// Configure offline mode if enabled
	if offlineMode {
		if err := validateOfflinePath(offlineModePath); err != nil {
			return err
		}

		log.Logger.Infow("configuring offline mode", "path", offlineModePath, "duration", offlineModeDuration)

		// Create output directory if it doesn't exist
		if err := os.MkdirAll(offlineModePath, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %v", err)
		}

		// Create health exporter configuration for offline mode
		cfg.HealthExporter = &config.HealthExporterConfig{
			OfflineMode:          true,
			OutputPath:           offlineModePath,
			Duration:             offlineModeDuration,
			OutputFormat:         offlineModeOutputFormat,
			IncludeMetrics:       cfg.HealthExporter.IncludeMetrics,
			IncludeEvents:        cfg.HealthExporter.IncludeEvents,
			IncludeMachineInfo:   cfg.HealthExporter.IncludeMachineInfo,
			IncludeComponentData: cfg.HealthExporter.IncludeComponentData,
			Interval:             cfg.HealthExporter.Interval,
			Timeout:              cfg.HealthExporter.Timeout,
			MetricsLookback:      cfg.HealthExporter.MetricsLookback,
			EventsLookback:       cfg.HealthExporter.EventsLookback,
			HealthCheckInterval:  cfg.HealthExporter.HealthCheckInterval,
		}
	}

	auditLogger := log.NewNopAuditLogger()
	if logFile != "" {
		logAuditFile := log.CreateAuditLogFilepath(logFile)
		auditLogger = log.NewAuditLogger(logAuditFile)
	}

	if err := cfg.Validate(); err != nil {
		return err
	}

	// Create context with appropriate timeout for offline mode
	var rootCtx context.Context
	var rootCancel context.CancelFunc

	if offlineMode && offlineModeDuration > 0 {
		// For duration-based offline mode, set context timeout
		rootCtx, rootCancel = context.WithTimeout(context.Background(), offlineModeDuration)
	} else {
		// For regular mode, use cancellable context
		rootCtx, rootCancel = context.WithCancel(context.Background())
	}
	defer rootCancel()

	start := time.Now()

	signals := make(chan os.Signal, 1)
	done := make(chan struct{})

	log.Logger.Infof("starting fleetint %v", version.Version)

	// Setup signal handling for graceful shutdown
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	server, err := server.New(rootCtx, auditLogger, cfg)
	if err != nil {
		return err
	}

	// Handle shutdown signals and offline mode duration
	go func() {
		if offlineMode && offlineModeDuration > 0 {
			// For duration-based offline mode, create a timer
			timer := time.NewTimer(offlineModeDuration)
			defer timer.Stop()

			select {
			case sig := <-signals:
				log.Logger.Warnw("received signal -- stopping server", "signal", sig)
			case <-timer.C:
				log.Logger.Infow("offline mode duration expired -- stopping server", "duration", offlineModeDuration)
			}
		} else {
			// For regular mode or static-only mode, just wait for signals
			sig := <-signals
			log.Logger.Warnw("received signal -- stopping server", "signal", sig)
		}

		rootCancel() // Cancel the context
		server.Stop()
		close(done)
	}()

	log.Logger.Infow("successfully booted", "tookSeconds", time.Since(start).Seconds())

	<-done

	return nil
}
