// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package metrics

import "time"

type Op struct {
	Since              time.Time
	RecordedSince      time.Time
	SelectedComponents map[string]struct{}
}

type OpOption func(*Op)

func (op *Op) ApplyOpts(opts []OpOption) error {
	for _, opt := range opts {
		opt(op)
	}

	return nil
}

func WithSince(t time.Time) OpOption {
	return func(op *Op) {
		op.Since = t
	}
}

// WithRecordedSince selects samples first stored at or after t, regardless of
// measurement time. Exporters use it to include late-arriving measurements.
// WithSince still filters by measurement time; both filters can be combined.
func WithRecordedSince(t time.Time) OpOption {
	return func(op *Op) {
		op.RecordedSince = t
	}
}

// WithComponents sets the components to be scraped.
// If no components are provided, all components will be scraped.
func WithComponents(components ...string) OpOption {
	return func(op *Op) {
		if len(components) > 0 && op.SelectedComponents == nil {
			op.SelectedComponents = make(map[string]struct{})
		}
		for _, component := range components {
			if component != "" {
				op.SelectedComponents[component] = struct{}{}
			}
		}
	}
}
