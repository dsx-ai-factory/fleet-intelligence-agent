// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

// Package syncer provides a syncer for the metrics.
package syncer

import (
	"context"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
)

type Syncer struct {
	ctx            context.Context
	cancel         context.CancelFunc
	scrapers       []pkgmetrics.Scraper
	store          pkgmetrics.Store
	scrapeInterval time.Duration
	purgeInterval  time.Duration
	retainDuration time.Duration
}

// NewSyncer creates one independent scrape loop per scraper. Each loop writes
// its results immediately, so a slow scraper does not delay other metrics.
// The syncer owns one purge loop for the shared store.
func NewSyncer(ctx context.Context, scrapers []pkgmetrics.Scraper, store pkgmetrics.Store, scrapeInterval time.Duration, purgeInterval time.Duration, retainDuration time.Duration) *Syncer {
	cctx, cancel := context.WithCancel(ctx)
	s := &Syncer{
		ctx:            cctx,
		cancel:         cancel,
		scrapers:       scrapers,
		store:          store,
		scrapeInterval: scrapeInterval,
		purgeInterval:  purgeInterval,
		retainDuration: retainDuration,
	}
	return s
}

func (s *Syncer) Start() {
	for _, scraper := range s.scrapers {
		if scraper == nil {
			continue
		}
		go s.runScraper(scraper)
	}

	go func() {
		ticker := time.NewTicker(s.purgeInterval)
		defer ticker.Stop()

		log.Logger.Infow("start purging metrics")
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}

			before := time.Now().UTC().Add(-s.retainDuration)
			if purged, err := s.store.Purge(s.ctx, before); err != nil {
				log.Logger.Errorw("failed to purge metrics", "error", err)
			} else {
				log.Logger.Infow("purged metrics", "purged", purged)
			}
		}
	}()
}

func (s *Syncer) runScraper(scraper pkgmetrics.Scraper) {
	ticker := time.NewTicker(s.scrapeInterval)
	defer ticker.Stop()

	log.Logger.Infow("start scraping and syncing metrics")
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}

		if err := s.sync(scraper); err != nil {
			log.Logger.Errorw("failed to sync metrics", "error", err)
		}
	}
}

func (s *Syncer) sync(scraper pkgmetrics.Scraper) error {
	ms, err := scraper.Scrape(s.ctx)
	if err != nil {
		return err
	}
	return s.store.Record(s.ctx, ms...)
}

func (s *Syncer) Stop() {
	log.Logger.Infow("stopping syncer")

	s.cancel()
}
