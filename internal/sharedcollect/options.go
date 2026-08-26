// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sharedcollect

import "time"

// Options configures the native sources owned by Collector.
type Options struct {
	DCGMAddress         string
	DCGMIsSocket        bool
	DCGMGroupNamePrefix string
	SourceTimeout       time.Duration
}
