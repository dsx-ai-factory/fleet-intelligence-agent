// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

// Package peermem monitors the peermem module status.
// Optional, enabled if the host has NVIDIA GPUs.
package peermem

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/kmsg"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

const Name = "accelerator-nvidia-peermem"

const defaultHealthCheckInterval = time.Minute

var _ components.Component = &component{}

type component struct {
	ctx    context.Context
	cancel context.CancelFunc

	healthCheckInterval time.Duration

	gpuProvider components.GPUDeviceProvider

	eventBucket eventstore.Bucket
	kmsgSyncer  *kmsg.Syncer

	checkLsmodPeermemModuleFunc func(ctx context.Context) (*LsmodPeermemModuleOutput, error)

	lastMu          sync.RWMutex
	lastCheckResult *checkResult
}

func New(gpudInstance *components.GPUdInstance) (components.Component, error) {
	cctx, ccancel := context.WithCancel(gpudInstance.RootCtx)
	healthCheckInterval := defaultHealthCheckInterval
	if gpudInstance.HealthCheckInterval > 0 {
		healthCheckInterval = gpudInstance.HealthCheckInterval
	}

	c := &component{
		ctx:    cctx,
		cancel: ccancel,

		healthCheckInterval: healthCheckInterval,

		gpuProvider: gpudInstance,

		checkLsmodPeermemModuleFunc: CheckLsmodPeermemModule,
	}

	if gpudInstance.EventStore != nil {
		var err error
		c.eventBucket, err = gpudInstance.EventStore.Bucket(Name)
		if err != nil {
			ccancel()
			return nil, err
		}

		if os.Geteuid() == 0 {
			c.kmsgSyncer, err = kmsg.NewSyncer(cctx, Match, c.eventBucket)
			if err != nil {
				ccancel()
				return nil, err
			}
		}
	}

	return c, nil
}

// InjectFault replaces the lsmod peermem module function with an error-returning version
func (c *component) InjectFault(errMsg string) {
	c.checkLsmodPeermemModuleFunc = func(ctx context.Context) (*LsmodPeermemModuleOutput, error) {
		return nil, fmt.Errorf("injected peermem fault: %s", errMsg)
	}
}

// ClearFault restores the original lsmod peermem module function
func (c *component) ClearFault() {
	c.checkLsmodPeermemModuleFunc = CheckLsmodPeermemModule
}

// InjectEvent injects an event into the peermem component's event bucket
func (c *component) InjectEvent(name, eventType, message string) error {
	if c.eventBucket == nil {
		return fmt.Errorf("peermem component has no event bucket")
	}

	event := eventstore.Event{
		Component: Name,
		Time:      time.Now().UTC(),
		Name:      name,
		Type:      eventType,
		Message:   message,
	}

	return c.eventBucket.Insert(context.Background(), event)
}

func (c *component) Name() string { return Name }

func (c *component) Tags() []string {
	return []string{
		"accelerator",
		"gpu",
		"nvidia",
		Name,
	}
}

func (c *component) IsSupported() bool {
	if c.gpuProvider == nil {
		return false
	}
	return len(c.gpuProvider.GPUDevices()) > 0
}

func (c *component) Start() error {
	go func() {
		ticker := time.NewTicker(c.healthCheckInterval)
		defer ticker.Stop()

		for {
			_ = c.Check()

			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (c *component) LastHealthStates() apiv1.HealthStates {
	c.lastMu.RLock()
	lastCheckResult := c.lastCheckResult
	c.lastMu.RUnlock()
	return lastCheckResult.HealthStates()
}

func (c *component) Events(ctx context.Context, since time.Time) (apiv1.Events, error) {
	if c.eventBucket == nil {
		return nil, nil
	}
	evs, err := c.eventBucket.Get(ctx, since)
	if err != nil {
		return nil, err
	}
	return evs.Events(), nil
}

func (c *component) Close() error {
	log.Logger.Debugw("closing component")

	c.cancel()

	if c.kmsgSyncer != nil {
		c.kmsgSyncer.Close()
	}
	if c.eventBucket != nil {
		c.eventBucket.Close()
	}

	return nil
}

func (c *component) Check() components.CheckResult {
	log.Logger.Infow("checking nvidia gpu peermem")

	cr := &checkResult{
		ts: time.Now().UTC(),
	}
	defer func() {
		c.lastMu.Lock()
		c.lastCheckResult = cr
		c.lastMu.Unlock()
	}()

	if c.gpuProvider == nil || len(c.gpuProvider.GPUDevices()) == 0 {
		cr.health = apiv1.HealthStateTypeHealthy
		cr.reason = "GPU is not detected by DCGM"
		return cr
	}

	cctx, ccancel := context.WithTimeout(c.ctx, 30*time.Second)
	cr.PeerMemModuleOutput, cr.err = c.checkLsmodPeermemModuleFunc(cctx)
	ccancel()
	if cr.err != nil {
		cr.health = apiv1.HealthStateTypeUnhealthy
		cr.reason = "error checking peermem"
		log.Logger.Warnw(cr.reason, "error", cr.err)
		return cr
	}

	cr.health = apiv1.HealthStateTypeHealthy
	if cr.PeerMemModuleOutput != nil && cr.PeerMemModuleOutput.IbcoreUsingPeermemModule {
		cr.reason = "ibcore successfully loaded peermem module"
	} else {
		cr.reason = "ibcore is not using peermem module"
	}

	return cr
}

var _ components.CheckResult = &checkResult{}

type checkResult struct {
	PeerMemModuleOutput *LsmodPeermemModuleOutput `json:"peer_mem_module_output,omitempty"`

	// timestamp of the last check
	ts time.Time
	// error from the last check
	err error

	// tracks the healthy evaluation result of the last check
	health apiv1.HealthStateType
	// tracks the reason of the last check
	reason string
}

func (cr *checkResult) ComponentName() string {
	return Name
}

func (cr *checkResult) String() string {
	if cr == nil {
		return ""
	}
	if cr.PeerMemModuleOutput == nil {
		return "no data"
	}

	return fmt.Sprintf("ibcore using peermem module: %t", cr.PeerMemModuleOutput.IbcoreUsingPeermemModule)
}

func (cr *checkResult) Summary() string {
	if cr == nil {
		return ""
	}
	return cr.reason
}

func (cr *checkResult) HealthStateType() apiv1.HealthStateType {
	if cr == nil {
		return ""
	}
	return cr.health
}

func (cr *checkResult) getError() string {
	if cr == nil || cr.err == nil {
		return ""
	}
	return cr.err.Error()
}

func (cr *checkResult) HealthStates() apiv1.HealthStates {
	if cr == nil {
		return apiv1.HealthStates{
			{
				Time:      metav1.NewTime(time.Now().UTC()),
				Component: Name,
				Name:      Name,
				Health:    apiv1.HealthStateTypeHealthy,
				Reason:    "no data yet",
			},
		}
	}

	state := apiv1.HealthState{
		Time:      metav1.NewTime(cr.ts),
		Component: Name,
		Name:      Name,
		Reason:    cr.reason,
		Error:     cr.getError(),
		Health:    cr.health,
	}

	if cr.PeerMemModuleOutput != nil {
		b, _ := json.Marshal(cr)
		state.ExtraInfo = map[string]string{"data": string(b)}
	}
	return apiv1.HealthStates{state}
}
