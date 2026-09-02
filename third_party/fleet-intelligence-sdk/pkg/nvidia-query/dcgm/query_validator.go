// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dcgm

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

// SentinelType indicates the semantic meaning of a sentinel value
type SentinelType int

const (
	SentinelNone            SentinelType = iota
	SentinelBlank                        // No data collected yet (transient)
	SentinelNotFound                     // Entity doesn't exist (permanent)
	SentinelNotSupported                 // GPU doesn't support (permanent)
	SentinelNotPermissioned              // Hostengine lacks permissions (check hostengine)
)

// CheckSentinelHelper performs TYPE-AWARE sentinel checking
// CRITICAL: Must check FieldType before attempting value conversion
func CheckSentinelHelper(val dcgm.FieldValue_v1) (SentinelType, bool) {
	switch val.FieldType {
	case dcgm.DCGM_FT_INT64:
		v := val.Int64()
		switch v {
		case dcgm.DCGM_FT_INT64_BLANK:
			return SentinelBlank, true
		case dcgm.DCGM_FT_INT64_NOT_FOUND:
			return SentinelNotFound, true
		case dcgm.DCGM_FT_INT64_NOT_SUPPORTED:
			return SentinelNotSupported, true
		case dcgm.DCGM_FT_INT64_NOT_PERMISSIONED:
			return SentinelNotPermissioned, true
		}

	case dcgm.DCGM_FT_DOUBLE:
		v := val.Float64()
		// CRITICAL: These are special float values, not literal -999999.0
		switch v {
		case dcgm.DCGM_FT_FP64_BLANK:
			return SentinelBlank, true
		case dcgm.DCGM_FT_FP64_NOT_FOUND:
			return SentinelNotFound, true
		case dcgm.DCGM_FT_FP64_NOT_SUPPORTED:
			return SentinelNotSupported, true
		case dcgm.DCGM_FT_FP64_NOT_PERMISSIONED:
			return SentinelNotPermissioned, true
		}

	case dcgm.DCGM_FT_STRING:
		v := val.String()
		switch v {
		case dcgm.DCGM_FT_STR_BLANK:
			return SentinelBlank, true
		case dcgm.DCGM_FT_STR_NOT_FOUND:
			return SentinelNotFound, true
		case dcgm.DCGM_FT_STR_NOT_SUPPORTED:
			return SentinelNotSupported, true
		case dcgm.DCGM_FT_STR_NOT_PERMISSIONED:
			return SentinelNotPermissioned, true
		}
	}

	return SentinelNone, false
}

// CheckSentinelV2Helper performs TYPE-AWARE sentinel checking for FieldValue_v2
// This is used by components that use GetValuesSince (e.g., xid component)
func CheckSentinelV2Helper(val dcgm.FieldValue_v2) (SentinelType, bool) {
	switch val.FieldType {
	case dcgm.DCGM_FT_INT64:
		v := val.Int64()
		switch v {
		case dcgm.DCGM_FT_INT64_BLANK:
			return SentinelBlank, true
		case dcgm.DCGM_FT_INT64_NOT_FOUND:
			return SentinelNotFound, true
		case dcgm.DCGM_FT_INT64_NOT_SUPPORTED:
			return SentinelNotSupported, true
		case dcgm.DCGM_FT_INT64_NOT_PERMISSIONED:
			return SentinelNotPermissioned, true
		}

	case dcgm.DCGM_FT_DOUBLE:
		v := val.Float64()
		// CRITICAL: These are special float values, not literal -999999.0
		switch v {
		case dcgm.DCGM_FT_FP64_BLANK:
			return SentinelBlank, true
		case dcgm.DCGM_FT_FP64_NOT_FOUND:
			return SentinelNotFound, true
		case dcgm.DCGM_FT_FP64_NOT_SUPPORTED:
			return SentinelNotSupported, true
		case dcgm.DCGM_FT_FP64_NOT_PERMISSIONED:
			return SentinelNotPermissioned, true
		}

	case dcgm.DCGM_FT_STRING:
		v := val.String()
		switch v {
		case dcgm.DCGM_FT_STR_BLANK:
			return SentinelBlank, true
		case dcgm.DCGM_FT_STR_NOT_FOUND:
			return SentinelNotFound, true
		case dcgm.DCGM_FT_STR_NOT_SUPPORTED:
			return SentinelNotSupported, true
		case dcgm.DCGM_FT_STR_NOT_PERMISSIONED:
			return SentinelNotPermissioned, true
		}
	}

	return SentinelNone, false
}

// CheckSentinel logs sentinel values with caller-provided context.
// Returns true if the value is a sentinel and should be skipped.
// args must be key/value pairs (e.g., "deviceID", id, "uuid", uuid).
func CheckSentinel(val dcgm.FieldValue_v1, args ...interface{}) bool {
	sentinelType, isSentinel := CheckSentinelHelper(val)
	if !isSentinel {
		return false
	}

	logFields := append(args, "fieldID", val.FieldID, "sentinel", sentinelType.String())
	switch sentinelType {
	case SentinelBlank:
		// Normal warmup, only log if sustained
	case SentinelNotSupported:
		log.Logger.Infow("field not supported", logFields...)
	case SentinelNotPermissioned:
		log.Logger.Warnw("field requires elevated permissions in hostengine", logFields...)
	case SentinelNotFound:
		log.Logger.Warnw("field entity not found", logFields...)
	default:
		log.Logger.Warnw("field has sentinel value", logFields...)
	}
	return true
}

// CheckSentinelV2 logs sentinel values for FieldValue_v2 with caller context.
// Returns true if the value is a sentinel and should be skipped.
// args must be key/value pairs (e.g., "entityID", id, "timestamp", ts).
func CheckSentinelV2(val dcgm.FieldValue_v2, args ...interface{}) bool {
	sentinelType, isSentinel := CheckSentinelV2Helper(val)
	if !isSentinel {
		return false
	}

	logFields := append(args, "fieldID", val.FieldID, "sentinel", sentinelType.String())
	switch sentinelType {
	case SentinelBlank:
		// Normal warmup, only log if sustained
	case SentinelNotSupported:
		log.Logger.Infow("field not supported", logFields...)
	case SentinelNotPermissioned:
		log.Logger.Warnw("field requires elevated permissions in hostengine", logFields...)
	case SentinelNotFound:
		log.Logger.Warnw("field entity not found", logFields...)
	default:
		log.Logger.Warnw("field has sentinel value", logFields...)
	}
	return true
}

// IsRestartRequired returns true if the DCGM error requires process restart.
// Fatal errors like connection loss or group destruction require clean restart.
func IsRestartRequired(err error) bool {
	code, ok := dcgmErrorCode(err)
	if !ok {
		return false
	}
	switch code {
	case dcgm.DCGM_ST_CONNECTION_NOT_VALID,
		dcgm.DCGM_ST_NOT_CONFIGURED,
		dcgm.DCGM_ST_NOT_WATCHED:
		return true
	default:
		return false
	}
}

// IsUnhealthyAPIError returns true if the DCGM API error indicates unhealthy state.
func IsUnhealthyAPIError(err error) bool {
	code, ok := dcgmErrorCode(err)
	if !ok {
		return false
	}
	switch code {
	case dcgm.DCGM_ST_NVML_ERROR,
		dcgm.DCGM_ST_GPU_IS_LOST,
		dcgm.DCGM_ST_RESET_REQUIRED,
		dcgm.DCGM_ST_GPU_NOT_SUPPORTED:
		return true
	default:
		return false
	}
}

// IsGPULostError reports whether err carries DCGM's GPU-lost status. Components
// that historically exposed recovery guidance use this in addition to the
// broader IsUnhealthyAPIError classification.
func IsGPULostError(err error) bool {
	code, ok := dcgmErrorCode(err)
	return ok && code == dcgm.DCGM_ST_GPU_IS_LOST
}

// IsResetRequiredError reports whether err carries DCGM's reset-required
// status. It is intentionally separate from IsUnhealthyAPIError so callers can
// preserve status-specific recovery guidance without changing common policy.
func IsResetRequiredError(err error) bool {
	code, ok := dcgmErrorCode(err)
	return ok && code == dcgm.DCGM_ST_RESET_REQUIRED
}

// IsTransientError returns true for transient DCGM errors (e.g., warmup).
func IsTransientError(err error) bool {
	code, ok := dcgmErrorCode(err)
	if !ok {
		return false
	}
	switch code {
	case dcgm.DCGM_ST_NO_DATA,
		dcgm.DCGM_ST_STALE_DATA:
		return true
	default:
		return false
	}
}

// AppendDCGMErrorType appends a human-readable DCGM error type to a reason string.
// If the error is not a DCGM error or is unknown, the original reason is returned.
func AppendDCGMErrorType(reason string, err error) string {
	if err == nil {
		return reason
	}
	var derr *dcgm.Error
	if !errors.As(err, &derr) {
		return reason
	}
	errorType := dcgmErrorTypeString(int32(derr.Code))
	if errorType == "" {
		return reason
	}
	return fmt.Sprintf("%s (dcgm_error=%s)", reason, errorType)
}

// ShouldRetry indicates if we should retry querying this field later
func (s SentinelType) ShouldRetry() bool {
	return s == SentinelBlank // Only BLANK is transient
}

// String returns human-readable description
func (s SentinelType) String() string {
	switch s {
	case SentinelBlank:
		return "BLANK (no data yet)"
	case SentinelNotFound:
		return "NOT_FOUND (entity missing)"
	case SentinelNotSupported:
		return "NOT_SUPPORTED (hardware doesn't support)"
	case SentinelNotPermissioned:
		return "NOT_PERMISSIONED (hostengine lacks CAP_SYS_ADMIN)"
	default:
		return "OK"
	}
}

func dcgmErrorCode(err error) (int32, bool) {
	if err == nil {
		return 0, false
	}
	var derr *dcgm.Error
	if !errors.As(err, &derr) {
		if code, ok := parseDCGMErrorCodeFromString(err); ok {
			return code, true
		}
		return 0, false
	}
	return int32(derr.Code), true
}

var dcgmErrorCodeRe = regexp.MustCompile(`(?i)error code (-?\d+)`)

func parseDCGMErrorCodeFromString(err error) (int32, bool) {
	matches := dcgmErrorCodeRe.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return 0, false
	}
	parsed, err := strconv.ParseInt(matches[1], 10, 32)
	if err != nil {
		return 0, false
	}
	return int32(parsed), true
}

func dcgmErrorTypeString(code int32) string {
	switch code {
	case dcgm.DCGM_ST_NVML_ERROR:
		return "NVML_ERROR"
	case dcgm.DCGM_ST_GPU_IS_LOST:
		return "GPU_IS_LOST"
	case dcgm.DCGM_ST_RESET_REQUIRED:
		return "RESET_REQUIRED"
	case dcgm.DCGM_ST_GPU_NOT_SUPPORTED:
		return "GPU_NOT_SUPPORTED"
	case dcgm.DCGM_ST_TIMEOUT:
		return "TIMEOUT"
	case dcgm.DCGM_ST_NVML_DRIVER_TIMEOUT:
		return "NVML_DRIVER_TIMEOUT"
	case dcgm.DCGM_ST_CONNECTION_NOT_VALID:
		return "CONNECTION_NOT_VALID"
	case dcgm.DCGM_ST_NOT_CONFIGURED:
		return "NOT_CONFIGURED"
	case dcgm.DCGM_ST_NOT_WATCHED:
		return "NOT_WATCHED"
	case dcgm.DCGM_ST_NO_DATA:
		return "NO_DATA"
	case dcgm.DCGM_ST_STALE_DATA:
		return "STALE_DATA"
	default:
		return ""
	}
}
