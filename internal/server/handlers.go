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

package server

import (
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	"github.com/gin-gonic/gin"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

type globalHandler struct {
	cfg *config.Config

	componentsRegistry components.Registry

	componentNamesMu sync.RWMutex
	componentNames   []string

	metricsStore pkgmetrics.Store

	gpudInstance *components.GPUdInstance
}

func newGlobalHandler(cfg *config.Config, componentsRegistry components.Registry, metricsStore pkgmetrics.Store, gpudInstance *components.GPUdInstance) *globalHandler {
	var componentNames []string
	for _, c := range componentsRegistry.All() {
		componentNames = append(componentNames, c.Name())
	}
	sort.Strings(componentNames)

	return &globalHandler{
		cfg:                cfg,
		componentsRegistry: componentsRegistry,
		componentNames:     componentNames,
		metricsStore:       metricsStore,
		gpudInstance:       gpudInstance,
	}
}

// clampStartTime ensures startTime is not older than the configured retention
// period. Queries that reach further back would scan data that has already been
// purged (or is about to be), wasting I/O and memory.
func (g *globalHandler) clampStartTime(startTime time.Time) time.Time {
	earliest := time.Now().Add(-g.cfg.RetentionPeriod.Duration)
	if startTime.Before(earliest) {
		return earliest
	}
	return startTime
}

func (g *globalHandler) getReqComponents(c *gin.Context) ([]string, error) {
	componentsStr := c.Query("components")
	if componentsStr == "" {
		g.componentNamesMu.RLock()
		defer g.componentNamesMu.RUnlock()
		return g.componentNames, nil
	}

	return []string{componentsStr}, nil
}

// getHealthStates returns the health states of components
func (g *globalHandler) getHealthStates(c *gin.Context) {
	components, err := g.getReqComponents(c)
	if err != nil {
		log.Logger.Warnw("failed to parse components", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid components parameter"})
		return
	}

	// Get current health states from components
	states := make(map[string]interface{})
	for _, comp := range g.componentsRegistry.All() {
		if len(components) > 0 {
			found := false
			for _, reqComp := range components {
				if comp.Name() == reqComp {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		states[comp.Name()] = comp.LastHealthStates()
	}

	c.JSON(http.StatusOK, states)
}

// getEvents returns component events
func (g *globalHandler) getEvents(c *gin.Context) {
	startTimeStr := c.Query("since")
	var startTime time.Time
	if startTimeStr != "" {
		startTimeInt, err := strconv.ParseInt(startTimeStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'since' parameter: must be a Unix timestamp"})
			return
		}
		startTime = time.Unix(startTimeInt, 0)
	} else {
		// Default to events from the last hour
		startTime = time.Now().Add(-time.Hour)
	}
	startTime = g.clampStartTime(startTime)

	components, err := g.getReqComponents(c)
	if err != nil {
		log.Logger.Warnw("failed to parse components", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid components parameter"})
		return
	}

	// Get events from components directly
	allEvents := make(map[string]interface{})
	for _, comp := range g.componentsRegistry.All() {
		if len(components) > 0 {
			found := false
			for _, reqComp := range components {
				if comp.Name() == reqComp {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		events, err := comp.Events(c.Request.Context(), startTime)
		if err != nil {
			log.Logger.Warnw("failed to get events", "component", comp.Name(), "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve events"})
			return
		}
		allEvents[comp.Name()] = events
	}

	c.JSON(http.StatusOK, allEvents)
}

// getInfo returns component information
func (g *globalHandler) getInfo(c *gin.Context) {
	components, err := g.getReqComponents(c)
	if err != nil {
		log.Logger.Warnw("failed to parse components", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid components parameter"})
		return
	}

	// Get basic component info
	info := make(map[string]interface{})
	for _, comp := range g.componentsRegistry.All() {
		if len(components) > 0 {
			found := false
			for _, reqComp := range components {
				if comp.Name() == reqComp {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		info[comp.Name()] = gin.H{
			"name":      comp.Name(),
			"tags":      comp.Tags(),
			"supported": comp.IsSupported(),
			"health":    comp.LastHealthStates(),
		}
	}

	c.JSON(http.StatusOK, info)
}

// getMetrics returns metrics from the metrics store
func (g *globalHandler) getMetrics(c *gin.Context) {
	startTimeStr := c.Query("startTime")
	var opts []pkgmetrics.OpOption

	if startTimeStr != "" {
		startTimeInt, err := strconv.ParseInt(startTimeStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid 'startTime' parameter: must be a Unix timestamp"})
			return
		}
		startTime := g.clampStartTime(time.Unix(startTimeInt, 0))
		opts = append(opts, pkgmetrics.WithSince(startTime))
	}

	componentsStr := c.Query("components")
	if componentsStr != "" {
		opts = append(opts, pkgmetrics.WithComponents(componentsStr))
	}

	metrics, err := g.metricsStore.Read(c.Request.Context(), opts...)
	if err != nil {
		log.Logger.Warnw("failed to read metrics", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve metrics"})
		return
	}

	c.JSON(http.StatusOK, metrics)
}

// machineInfo returns basic machine information
func (g *globalHandler) machineInfo(c *gin.Context) {
	if g.gpudInstance == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gpud instance not available"})
		return
	}

	info := gin.H{
		"machine_id": g.gpudInstance.MachineID,
		"service":    "fleetint",
	}

	info["nvidia_available"] = len(g.gpudInstance.GPUDevices()) > 0

	c.JSON(http.StatusOK, info)
}
