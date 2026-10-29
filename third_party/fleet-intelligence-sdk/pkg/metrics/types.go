// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package metrics

import (
	"context"
	"time"
)

// MetricComponentLabelKey is the key for the component of the metric.
const MetricComponentLabelKey = "gpud_component"

type MetricType string

const (
	MetricTypeGauge   MetricType = "gauge"
	MetricTypeCounter MetricType = "counter"
)

// Metric represents a metric row in the database table.
type Metric struct {
	// UnixMilliseconds is the source measurement time when supplied; otherwise
	// it is the scrape time. Repeated source samples keep the same timestamp.
	UnixMilliseconds int64 `json:"unix_milliseconds"`
	// Component represents the name of the component this metric belongs to.
	Component string `json:"component"`
	// Name represents the name of the metric.
	Name string `json:"name"`
	// Type represents the Prometheus metric type.
	Type MetricType `json:"type,omitempty"`
	// Value represents the numeric value of the metric.
	Value float64 `json:"value"`

	// Labels represents all the labels of the metric.
	Labels map[string]string `json:"labels,omitempty"`
}

// Metrics is a slice of Metric.
type Metrics []Metric

// Recorder defines the metrics recorder interface.
type Recorder interface {
	// Start starts the periodic metrics recorder.
	Start()
}

// Scraper defines the metrics scraper interface.
type Scraper interface {
	Scrape(context.Context) (Metrics, error)
}

// Store defines the metrics store interface.
type Store interface {
	// Record records metric data points.
	Record(ctx context.Context, ms ...Metric) error

	// Read returns data points matching the options. WithSince filters by
	// measurement time; WithRecordedSince filters by first receipt time.
	// Without options, Read returns all metrics.
	Read(ctx context.Context, opts ...OpOption) (Metrics, error)

	// Purge removes data points first recorded before the given time. Legacy
	// rows without a receipt timestamp use their measurement time.
	Purge(ctx context.Context, before time.Time) (int, error)
}
