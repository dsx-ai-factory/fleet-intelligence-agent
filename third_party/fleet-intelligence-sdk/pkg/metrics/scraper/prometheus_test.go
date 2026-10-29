// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package scraper

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

func TestPrometheusScraperMeasurementTimestampAndFallback(t *testing.T) {
	for _, sourceTime := range []int64{time.Now().Add(-time.Hour).UnixMicro(), 0, -1} {
		reg := prometheus.NewRegistry()
		vec := pkgmetrics.NewSampledGaugeVec(prometheus.GaugeOpts{Name: "measured_value", Help: "source value"},
			[]string{pkgmetrics.MetricComponentLabelKey})
		require.NoError(t, reg.Register(vec))
		vec.With(prometheus.Labels{pkgmetrics.MetricComponentLabelKey: "test"}).SetWithTimestamp(42, sourceTime)
		scraper, err := NewPrometheusScraper(reg)
		require.NoError(t, err)
		before := time.Now().UnixMilli()
		ms, err := scraper.Scrape(context.Background())
		require.NoError(t, err)
		require.Len(t, ms, 1)
		if sourceTime > 0 {
			require.Equal(t, sourceTime/1000, ms[0].UnixMilliseconds)
		} else {
			require.GreaterOrEqual(t, ms[0].UnixMilliseconds, before)
			require.LessOrEqual(t, ms[0].UnixMilliseconds, time.Now().UnixMilli())
		}
	}
}

func TestPrometheusScraper(t *testing.T) {
	t.Parallel()

	lastUpdateUnixSeconds := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "gpud",
			Name:      "last_update_unix_seconds",
			Help:      "tracks the last update time in unix seconds",
		},
	)
	currentCelsius := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "test",
			Name:      "current_celsius",
			Help:      "tracks the current temperature in celsius",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "label_uuid"},
	)
	slowdownUsedPercent := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "",
			Subsystem: "test",
			Name:      "slowdown_used_percent",
			Help:      "tracks the percentage of slowdown used",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "label_uuid"},
	).MustCurryWith(prometheus.Labels{
		pkgmetrics.MetricComponentLabelKey: "component-1",
	})
	insertUpdateTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "sqlite",
			Subsystem: "insert_update",
			Name:      "total",
			Help:      "total number of inserts and updates",
		},
		[]string{pkgmetrics.MetricComponentLabelKey},
	).MustCurryWith(prometheus.Labels{
		pkgmetrics.MetricComponentLabelKey: "component-1",
	})

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(lastUpdateUnixSeconds))
	require.NoError(t, reg.Register(currentCelsius))
	require.NoError(t, reg.Register(slowdownUsedPercent))
	require.NoError(t, reg.Register(insertUpdateTotal))

	scraper, err := NewPrometheusScraper(reg)
	require.NoError(t, err)
	require.NotNil(t, scraper)

	// should not be included since the component label does not exist
	lastUpdateUnixSeconds.Set(123)
	currentCelsius.WithLabelValues("component-1", "GPU-0").Set(100)
	slowdownUsedPercent.With(prometheus.Labels{"label_uuid": "GPU-0"}).Set(98)
	insertUpdateTotal.With(prometheus.Labels{}).Inc()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ms, err := scraper.Scrape(ctx)
	require.NoError(t, err)
	require.NotNil(t, ms)
	require.Equal(t, 3, len(ms))

	sort.Slice(ms, func(i, j int) bool {
		return ms[i].Name < ms[j].Name
	})

	require.Equal(t, "component-1", ms[0].Component)
	require.Equal(t, "sqlite_insert_update_total", ms[0].Name)
	require.Equal(t, pkgmetrics.MetricTypeCounter, ms[0].Type)
	require.Nil(t, ms[0].Labels, "Expected no labels for the first metric")
	require.Equal(t, float64(1), ms[0].Value)

	require.Equal(t, "component-1", ms[1].Component)
	require.Equal(t, "test_current_celsius", ms[1].Name)
	require.Equal(t, pkgmetrics.MetricTypeGauge, ms[1].Type)
	require.Equal(t, "GPU-0", ms[1].Labels["label_uuid"])
	require.Equal(t, float64(100), ms[1].Value)

	require.Equal(t, "component-1", ms[2].Component)
	require.Equal(t, "test_slowdown_used_percent", ms[2].Name)
	require.Equal(t, pkgmetrics.MetricTypeGauge, ms[2].Type)
	require.Equal(t, "GPU-0", ms[2].Labels["label_uuid"])
	require.Equal(t, float64(98), ms[2].Value)
}

// mockGathererWithError implements prometheus.Gatherer interface and always returns an error
type mockGathererWithError struct {
	err error
}

func (m *mockGathererWithError) Gather() ([]*dto.MetricFamily, error) {
	return nil, m.err
}

type mockGatherer struct {
	metricFamilies []*dto.MetricFamily
}

func (m *mockGatherer) Gather() ([]*dto.MetricFamily, error) {
	return m.metricFamilies, nil
}

func TestPrometheusScraper_GatherError(t *testing.T) {
	t.Parallel()

	// Create a mock gatherer that always returns an error
	expectedErr := errors.New("gather error")
	mockGatherer := &mockGathererWithError{err: expectedErr}

	// Create scraper with our error-returning gatherer
	scraper := &promScraper{
		gatherer: mockGatherer,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Call Scrape and verify it returns the expected error
	metrics, err := scraper.Scrape(ctx)
	require.Error(t, err)
	require.Equal(t, expectedErr, err)
	require.Nil(t, metrics)
}

func TestPrometheusScraper_SkipsUnsupportedMetricKinds(t *testing.T) {
	t.Parallel()

	scraper := &promScraper{
		gatherer: &mockGatherer{
			metricFamilies: []*dto.MetricFamily{
				{
					Name: stringPtr("test_summary"),
					Type: dto.MetricType_SUMMARY.Enum(),
					Metric: []*dto.Metric{
						{
							Label: []*dto.LabelPair{
								{
									Name:  stringPtr(pkgmetrics.MetricComponentLabelKey),
									Value: stringPtr("component-1"),
								},
							},
							Summary: &dto.Summary{},
						},
					},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	metrics, err := scraper.Scrape(ctx)
	require.NoError(t, err)
	require.Empty(t, metrics)
}

func TestPrometheusScraper_NilGatherer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a scraper with nil gatherer
	scraper := &promScraper{
		gatherer: nil,
	}

	// Call Scrape and verify it returns nil without error
	metrics, err := scraper.Scrape(ctx)
	require.NoError(t, err)
	require.Nil(t, metrics)
}

func stringPtr(s string) *string {
	return &s
}

func TestPrometheusScraper_NilScraper(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a nil scraper
	var scraper *promScraper

	// Call Scrape and verify it returns nil without error
	metrics, err := scraper.Scrape(ctx)
	require.NoError(t, err)
	require.Nil(t, metrics)
}

func TestPrometheusScraper_EmptyMetrics(t *testing.T) {
	t.Parallel()

	// Create an empty registry
	reg := prometheus.NewRegistry()

	scraper, err := NewPrometheusScraper(reg)
	require.NoError(t, err)
	require.NotNil(t, scraper)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Scrape with no metrics registered
	ms, err := scraper.Scrape(ctx)
	require.NoError(t, err)
	require.Empty(t, ms)
}

func TestPrometheusScraper_MultipleMetricTypes(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	// Add a counter
	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "test",
			Subsystem: "operations",
			Name:      "total",
			Help:      "Total operations performed",
		},
		[]string{pkgmetrics.MetricComponentLabelKey},
	)
	require.NoError(t, reg.Register(counter))
	counter.WithLabelValues("component1").Inc()

	// Add a gauge
	gauge := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "test",
			Subsystem: "resources",
			Name:      "utilization",
			Help:      "Resource utilization",
		},
		[]string{pkgmetrics.MetricComponentLabelKey, "label_uuid"},
	)
	require.NoError(t, reg.Register(gauge))
	gauge.WithLabelValues("component1", "resource1").Set(75.5)

	// Add a histogram
	histogram := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "test",
			Subsystem: "latency",
			Name:      "seconds",
			Help:      "Request latency distribution",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{pkgmetrics.MetricComponentLabelKey},
	)
	require.NoError(t, reg.Register(histogram))
	histogram.WithLabelValues("component1").Observe(0.57)

	// Add a summary
	summary := prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Namespace: "test",
			Subsystem: "response",
			Name:      "time_seconds",
			Help:      "Response time summary",
		},
		[]string{pkgmetrics.MetricComponentLabelKey},
	)
	require.NoError(t, reg.Register(summary))
	summary.WithLabelValues("component1").Observe(0.123)

	scraper, err := NewPrometheusScraper(reg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Scrape and verify metrics
	ms, err := scraper.Scrape(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, ms)

	require.Len(t, ms, 2, "Only counter and gauge metrics should be returned")

	// Verify the counter and gauge are included
	var foundCounter, foundGauge bool
	for _, m := range ms {
		if m.Name == "test_operations_total" && m.Component == "component1" {
			foundCounter = true
			require.Equal(t, pkgmetrics.MetricTypeCounter, m.Type)
			require.Equal(t, float64(1), m.Value)
		}
		if m.Name == "test_resources_utilization" && m.Component == "component1" {
			foundGauge = true
			require.Equal(t, pkgmetrics.MetricTypeGauge, m.Type)
			if labels := m.Labels; labels != nil {
				require.Equal(t, "resource1", labels["label_uuid"])
			}
			require.Equal(t, 75.5, m.Value)
		}
	}
	require.True(t, foundCounter, "Counter metric not found")
	require.True(t, foundGauge, "Gauge metric not found")
}
