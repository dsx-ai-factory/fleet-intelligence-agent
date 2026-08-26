// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import (
	pkglog "github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"

	"github.com/dsx-ai-factory/health-validation/collect/observation"
)

func logCollectionErrors(kind string, observations []*observation.Observation) {
	if len(observations) == 0 {
		return
	}
	pkglog.Logger.Warnw("shared collection reported errors", "kind", kind, "count", len(observations))
	for _, current := range observations {
		if current == nil || current.GetCollectionError() == nil {
			continue
		}
		failure := current.GetCollectionError()
		entityID := ""
		if current.GetEntity() != nil {
			entityID = current.GetEntity().GetId()
		}
		pkglog.Logger.Debugw(
			"shared collection error",
			"kind", kind,
			"signal_id", current.GetSignalId(),
			"source", failure.GetSource(),
			"entity_id", entityID,
			"operation", failure.GetOperation(),
			"category", failure.GetCategory().String(),
			"source_error", failure.GetSourceError(),
			"detail", failure.GetDetail(),
		)
	}
}
