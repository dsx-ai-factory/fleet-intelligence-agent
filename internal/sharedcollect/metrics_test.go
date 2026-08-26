// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func TestMetricsFromObservationsPreservesFIContract(t *testing.T) {
	timestamp := time.Date(2026, time.August, 25, 12, 0, 0, 0, time.UTC)
	entity := &observation.Entity{Type: "gpu", Id: "GPU-test"}
	observations := []*observation.Observation{
		testIntObservation(observation.SignalGPUInventoryIndex, sourceNVML, entity, timestamp, 7),
		testIntObservation(observation.SignalGPUInventoryIndex, sourceDCGM, entity, timestamp, 3),
		testDoubleObservation(observation.SignalPowerDraw, sourceNVML, entity, timestamp, 999),
		testDoubleObservation(observation.SignalPowerDraw, sourceDCGM, entity, timestamp, 125.5),
		testIntObservation(observation.SignalPowerViolationDuration, sourceDCGM, entity, timestamp, 42),
		observation.NewCollectionErrorObservation(
			observation.SignalGPUTemperatureCelsius,
			sourceDCGM,
			entity,
			timestamppb.New(timestamp),
			observation.UnitForSignal(observation.SignalGPUTemperatureCelsius),
			&observation.CollectionError{Category: observation.CollectionErrorCategoryUnavailable, Detail: "GPU is lost"},
		),
		{
			SignalId:   observation.SignalPowerLimitEnforced,
			Source:     sourceDCGM,
			Entity:     entity,
			ObservedAt: timestamppb.New(timestamp),
			Unit:       observation.UnitForSignal(observation.SignalPowerLimitEnforced),
			Outcome: &observation.Observation_Value{Value: &observation.Value{
				Kind: &observation.Value_StringValue{StringValue: "not numeric"},
			}},
		},
	}

	metrics, collectionErrors, projectionErrors := MetricsFromObservations(observations)
	require.Len(t, metrics, 2)
	require.Len(t, collectionErrors, 1)
	require.Len(t, projectionErrors, 1)

	require.Equal(t, pkgmetrics.Metric{
		UnixMilliseconds: timestamp.UnixMilli(),
		Component:        componentPower,
		Name:             "dcgm_fi_dev_power_usage",
		Type:             pkgmetrics.MetricTypeGauge,
		Value:            125.5,
		Labels:           map[string]string{"uuid": "GPU-test", "gpu": "3"},
	}, metrics[0])
	require.Equal(t, pkgmetrics.MetricTypeGauge, metrics[1].Type)
	require.Equal(t, "dcgm_fi_dev_power_violation", metrics[1].Name)
}

func TestMetricDefinitionsAreUnique(t *testing.T) {
	require.Len(t, metricDefinitions, 57)
	signals := make(map[observation.SignalID]struct{}, len(metricDefinitions))
	names := make(map[string]struct{}, len(metricDefinitions))
	for _, definition := range metricDefinitions {
		require.Equalf(t, pkgmetrics.MetricTypeGauge, definition.metricType, "legacy metric %q must remain a gauge", definition.name)

		_, duplicateSignal := signals[definition.signalID]
		require.Falsef(t, duplicateSignal, "duplicate signal %q", definition.signalID)
		signals[definition.signalID] = struct{}{}

		_, duplicateName := names[definition.name]
		require.Falsef(t, duplicateName, "duplicate metric name %q", definition.name)
		names[definition.name] = struct{}{}
	}
}

func testIntObservation(signalID, source string, entity *observation.Entity, observedAt time.Time, value int64) *observation.Observation {
	return &observation.Observation{
		SignalId:   signalID,
		Source:     source,
		Entity:     entity,
		ObservedAt: timestamppb.New(observedAt),
		Unit:       observation.UnitForSignal(signalID),
		Outcome: &observation.Observation_Value{Value: &observation.Value{
			Kind: &observation.Value_IntValue{IntValue: value},
		}},
	}
}

func testDoubleObservation(signalID, source string, entity *observation.Entity, observedAt time.Time, value float64) *observation.Observation {
	return &observation.Observation{
		SignalId:   signalID,
		Source:     source,
		Entity:     entity,
		ObservedAt: timestamppb.New(observedAt),
		Unit:       observation.UnitForSignal(signalID),
		Outcome: &observation.Observation_Value{Value: &observation.Value{
			Kind: &observation.Value_DoubleValue{DoubleValue: value},
		}},
	}
}

func testStringObservation(signalID, source string, entity *observation.Entity, observedAt time.Time, value string) *observation.Observation {
	return &observation.Observation{
		SignalId:   signalID,
		Source:     source,
		Entity:     entity,
		ObservedAt: timestamppb.New(observedAt),
		Unit:       observation.UnitForSignal(signalID),
		Outcome: &observation.Observation_Value{Value: &observation.Value{
			Kind: &observation.Value_StringValue{StringValue: value},
		}},
	}
}
