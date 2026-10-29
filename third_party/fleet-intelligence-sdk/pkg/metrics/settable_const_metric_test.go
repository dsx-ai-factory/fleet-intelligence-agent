// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package metrics

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestSampledMetricExposesMeasurementTimeOverHTTP(t *testing.T) {
	reg := prometheus.NewRegistry()
	vec := NewSampledGaugeVec(prometheus.GaugeOpts{Name: "measurement", Help: "source value"}, nil)
	require.NoError(t, reg.Register(vec))
	vec.With(nil).SetWithTimestamp(42, 1_793_188_800_123_456)
	response := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), "measurement 42 1793188800123\n")
}

func TestSettableCounterVecEmitsCounterMetric(t *testing.T) {
	registry := prometheus.NewRegistry()
	counter := NewSettableCounterVec(
		prometheus.CounterOpts{
			Name: "external_absolute_total",
			Help: "external absolute counter",
		},
		[]string{MetricComponentLabelKey, "device"},
	).MustCurryWith(prometheus.Labels{MetricComponentLabelKey: "component1"})

	if err := registry.Register(counter); err != nil {
		t.Fatalf("Register() failed: %v", err)
	}

	counter.With(prometheus.Labels{"device": "gpu0"}).Set(42)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() failed: %v", err)
	}

	family := findMetricFamily(families, "external_absolute_total")
	if family == nil {
		t.Fatalf("metric family was not gathered")
	}
	if family.GetType() != dto.MetricType_COUNTER {
		t.Fatalf("metric type = %v, want %v", family.GetType(), dto.MetricType_COUNTER)
	}
	if got := family.GetMetric()[0].GetCounter().GetValue(); got != 42 {
		t.Fatalf("counter value = %v, want 42", got)
	}
}

func TestSampledMetricsPreserveMeasurementTime(t *testing.T) {
	for _, counter := range []bool{false, true} {
		t.Run(fmt.Sprintf("counter=%t", counter), func(t *testing.T) {
			vec := NewSampledGaugeVec(prometheus.GaugeOpts{Name: "source_sample", Help: "source sample"}, []string{"device"})
			kind := dto.MetricType_GAUGE
			if counter {
				vec = NewSettableCounterVec(prometheus.CounterOpts{Name: "source_sample", Help: "source sample"}, []string{"device"})
				kind = dto.MetricType_COUNTER
			}
			reg := prometheus.NewRegistry()
			require.NoError(t, reg.Register(vec))
			metric := vec.With(prometheus.Labels{"device": "gpu0"})
			ts := time.Date(2026, 10, 28, 12, 0, 0, 123456000, time.UTC).UnixMicro()
			for _, sourceTime := range []int64{ts, ts, ts + 60_000_000, 0, -1} {
				metric.SetWithTimestamp(42, sourceTime)
				families, err := reg.Gather()
				require.NoError(t, err)
				require.Len(t, families, 1)
				require.Equal(t, kind, families[0].GetType())
				sample := families[0].GetMetric()[0]
				if sourceTime > 0 {
					require.Equal(t, sourceTime/1000, sample.GetTimestampMs())
				} else {
					require.Nil(t, sample.TimestampMs, "missing times must use the scraper fallback")
				}
			}
			metric.SetWithTimestamp(43, ts)
			metric.Set(44)
			families, err := reg.Gather()
			require.NoError(t, err)
			require.Nil(t, families[0].GetMetric()[0].TimestampMs, "Set must clear a previous explicit timestamp")
		})
	}
}

func TestSampledMetricsSetAndGatherAreAtomic(t *testing.T) {
	vec := NewSampledGaugeVec(prometheus.GaugeOpts{Name: "atomic_sample", Help: "source sample"}, nil)
	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(vec))
	metric := vec.With(nil)
	var writers sync.WaitGroup
	for writer := range 2 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for range 100 {
				value := int64(writer + 1)
				metric.SetWithTimestamp(float64(value), value*1000)
			}
		}()
	}
	for range 100 {
		families, err := reg.Gather()
		require.NoError(t, err)
		if len(families) == 0 {
			continue
		}
		sample := families[0].GetMetric()[0]
		require.Equal(t, int64(sample.GetGauge().GetValue()), sample.GetTimestampMs())
	}
	writers.Wait()
}

func TestSettableCounterVecResetAndDelete(t *testing.T) {
	counter := NewSettableCounterVec(
		prometheus.CounterOpts{
			Name: "resettable_external_absolute_total",
			Help: "external absolute counter",
		},
		[]string{"device"},
	)

	labels := prometheus.Labels{"device": "gpu0"}
	counter.With(labels).Set(42)

	if !counter.Delete(labels) {
		t.Fatalf("Delete() = false, want true")
	}
	if counter.Delete(labels) {
		t.Fatalf("Delete() after delete = true, want false")
	}

	counter.With(labels).Set(43)
	counter.Reset()
	if counter.Delete(labels) {
		t.Fatalf("Delete() after reset = true, want false")
	}
}

func findMetricFamily(families []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	return nil
}
