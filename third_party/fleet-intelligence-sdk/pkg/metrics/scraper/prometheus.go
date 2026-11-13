// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

// Package scraper scrapes internal GPUd metrics from Prometheus.
package scraper

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

var _ pkgmetrics.Scraper = &promScraper{}

// NewPrometheusScraper creates a new Prometheus scraper.
// It uses the given prometheus.Gatherer to scrape metrics.
func NewPrometheusScraper(gatherer prometheus.Gatherer) (pkgmetrics.Scraper, error) {
	return &promScraper{
		gatherer: gatherer,
	}, nil
}

type promScraper struct {
	gatherer prometheus.Gatherer
}

func (s *promScraper) Scrape(_ context.Context) (pkgmetrics.Metrics, error) {
	if s == nil || s.gatherer == nil {
		return nil, nil
	}

	gathered, err := s.gatherer.Gather()
	if err != nil {
		return nil, err
	}

	log.Logger.Infow("scraping prometheus metrics")
	now := time.Now().UTC().UnixMilli()

	ms := make(pkgmetrics.Metrics, 0, len(gathered))
	for _, metricFamily := range gathered {
		for _, mtRaw := range metricFamily.GetMetric() {
			m := pkgmetrics.Metric{
				UnixMilliseconds: now,
				Name:             metricFamily.GetName(),
			}
			// Source collectors can supply the measurement time. Ordinary metrics
			// and invalid source timestamps retain the observation-time fallback.
			if mtRaw.GetTimestampMs() > 0 {
				m.UnixMilliseconds = mtRaw.GetTimestampMs()
			}

			labels := make(map[string]string, 0)
			for _, label := range mtRaw.GetLabel() {
				labelName := label.GetName()
				labelValue := label.GetValue()

				if labelName == pkgmetrics.MetricComponentLabelKey {
					m.Component = labelValue
					continue
				}
				labels[labelName] = labelValue
			}
			if len(labels) > 0 {
				m.Labels = labels
			}

			if m.Component == "" {
				continue
			}

			// for now, only support counter and gauge
			switch {
			case mtRaw.GetCounter() != nil:
				m.Type = pkgmetrics.MetricTypeCounter
				m.Value = mtRaw.GetCounter().GetValue()
			case mtRaw.GetGauge() != nil:
				m.Type = pkgmetrics.MetricTypeGauge
				m.Value = mtRaw.GetGauge().GetValue()
			default:
				continue
			}

			ms = append(ms, m)
		}
	}

	return ms, nil
}
