// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// SettableConstMetricVec stores externally-collected absolute values and emits
// them as constant Prometheus metrics with the configured ValueType.
type SettableConstMetricVec struct {
	opts           prometheus.Opts
	desc           *prometheus.Desc
	valueType      prometheus.ValueType
	variableLabels []string
	constLabels    prometheus.Labels

	mu      sync.RWMutex
	samples map[string]settableConstMetricSample
}

type settableConstMetricSample struct {
	labelValues []string
	value       float64
	// timestampMicroseconds is the source measurement time; zero uses scrape time.
	timestampMicroseconds int64
}

// NewSampledGaugeVec creates a gauge collector for externally measured samples.
// SetWithTimestamp preserves source measurement times; Set uses scrape time.
func NewSampledGaugeVec(opts prometheus.GaugeOpts, variableLabels []string) *SettableConstMetricVec {
	return newSettableConstMetricVec(prometheus.Opts(opts), prometheus.GaugeValue, variableLabels, nil)
}

// NewSettableCounterVec creates a collector for counters whose absolute values
// are read from an external source.
func NewSettableCounterVec(opts prometheus.CounterOpts, variableLabels []string) *SettableConstMetricVec {
	return newSettableConstMetricVec(prometheus.Opts(opts), prometheus.CounterValue, variableLabels, nil)
}

func newSettableConstMetricVec(opts prometheus.Opts, valueType prometheus.ValueType, variableLabels []string, constLabels prometheus.Labels) *SettableConstMetricVec {
	copiedVariableLabels := append([]string(nil), variableLabels...)
	copiedConstLabels := mergeLabels(opts.ConstLabels, constLabels)

	return &SettableConstMetricVec{
		opts:           opts,
		desc:           newConstMetricDesc(opts, copiedVariableLabels, copiedConstLabels),
		valueType:      valueType,
		variableLabels: copiedVariableLabels,
		constLabels:    copiedConstLabels,
		samples:        make(map[string]settableConstMetricSample),
	}
}

// MustCurryWith returns a collector with the provided labels emitted as constant
// labels. It panics if any provided label is not one of the variable labels.
func (v *SettableConstMetricVec) MustCurryWith(labels prometheus.Labels) *SettableConstMetricVec {
	if v == nil {
		panic("nil SettableConstMetricVec")
	}

	curriedLabels := copyLabels(v.constLabels)
	if curriedLabels == nil {
		curriedLabels = make(prometheus.Labels, len(labels))
	}
	for name, value := range labels {
		if !containsLabel(v.variableLabels, name) {
			panic(fmt.Sprintf("unknown label %q", name))
		}
		curriedLabels[name] = value
	}

	var remainingLabels []string
	for _, name := range v.variableLabels {
		if _, ok := labels[name]; ok {
			continue
		}
		remainingLabels = append(remainingLabels, name)
	}

	return newSettableConstMetricVec(v.opts, v.valueType, remainingLabels, curriedLabels)
}

// With returns a handle for setting one labeled metric sample.
func (v *SettableConstMetricVec) With(labels prometheus.Labels) *SettableConstMetric {
	if v == nil {
		panic("nil SettableConstMetricVec")
	}

	labelValues := make([]string, 0, len(v.variableLabels))
	for _, name := range v.variableLabels {
		value, ok := labels[name]
		if !ok {
			panic(fmt.Sprintf("missing label %q", name))
		}
		labelValues = append(labelValues, value)
	}
	for name := range labels {
		if !containsLabel(v.variableLabels, name) {
			panic(fmt.Sprintf("unknown label %q", name))
		}
	}

	return &SettableConstMetric{
		vec:         v,
		labelValues: labelValues,
		key:         strings.Join(labelValues, "\xff"),
	}
}

// Delete removes the sample for the provided label set.
func (v *SettableConstMetricVec) Delete(labels prometheus.Labels) bool {
	metric := v.With(labels)

	v.mu.Lock()
	defer v.mu.Unlock()

	if _, ok := v.samples[metric.key]; !ok {
		return false
	}
	delete(v.samples, metric.key)
	return true
}

// Reset removes all samples from the collector.
func (v *SettableConstMetricVec) Reset() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.samples = make(map[string]settableConstMetricSample)
}

// Describe implements prometheus.Collector.
func (v *SettableConstMetricVec) Describe(ch chan<- *prometheus.Desc) {
	ch <- v.desc
}

// Collect implements prometheus.Collector.
func (v *SettableConstMetricVec) Collect(ch chan<- prometheus.Metric) {
	v.mu.RLock()
	keys := make([]string, 0, len(v.samples))
	for key := range v.samples {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	samples := make([]settableConstMetricSample, 0, len(keys))
	for _, key := range keys {
		samples = append(samples, v.samples[key])
	}
	v.mu.RUnlock()

	for _, sample := range samples {
		metric, err := prometheus.NewConstMetric(v.desc, v.valueType, sample.value, sample.labelValues...)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(v.desc, err)
			continue
		}
		if sample.timestampMicroseconds > 0 {
			metric = prometheus.NewMetricWithTimestamp(time.UnixMicro(sample.timestampMicroseconds), metric)
		}
		ch <- metric
	}
}

// SettableConstMetric is a handle for one labeled sample.
type SettableConstMetric struct {
	vec         *SettableConstMetricVec
	labelValues []string
	key         string
}

// Set stores the current absolute value for this sample.
func (m *SettableConstMetric) Set(value float64) {
	m.SetWithTimestamp(value, 0)
}

// SetWithTimestamp atomically stores the value and its Unix measurement time in
// microseconds, as supplied by DCGM. Prometheus exposes millisecond precision.
// A nonpositive timestamp omits the explicit time, so scrapers use observation
// time. Repeated source samples retain the same measurement timestamp.
func (m *SettableConstMetric) SetWithTimestamp(value float64, timestampMicroseconds int64) {
	m.vec.mu.Lock()
	defer m.vec.mu.Unlock()
	m.vec.samples[m.key] = settableConstMetricSample{
		labelValues:           append([]string(nil), m.labelValues...),
		value:                 value,
		timestampMicroseconds: timestampMicroseconds,
	}
}

func newConstMetricDesc(opts prometheus.Opts, variableLabels []string, constLabels prometheus.Labels) *prometheus.Desc {
	return prometheus.NewDesc(
		prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name),
		opts.Help,
		variableLabels,
		constLabels,
	)
}

func containsLabel(labels []string, name string) bool {
	for _, label := range labels {
		if label == name {
			return true
		}
	}
	return false
}

func copyLabels(labels prometheus.Labels) prometheus.Labels {
	if labels == nil {
		return nil
	}
	copied := make(prometheus.Labels, len(labels))
	for name, value := range labels {
		copied[name] = value
	}
	return copied
}

func mergeLabels(labelSets ...prometheus.Labels) prometheus.Labels {
	var merged prometheus.Labels
	for _, labels := range labelSets {
		for name, value := range labels {
			if merged == nil {
				merged = make(prometheus.Labels)
			}
			merged[name] = value
		}
	}
	return merged
}
