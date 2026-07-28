// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

package sakauth

import (
	"fmt"
	"net/url"

	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/config/configtls"
)

// Config holds the configuration for the Fleet Intelligence auth extension.
type Config struct {
	// EnrollEndpoint is the backend enrollment URL used to obtain a JWT.
	// Example: https://backend/api/v1/agent/enroll
	EnrollEndpoint string `mapstructure:"enroll_endpoint"`

	// SAKToken is the Service API Key sent as Authorization: Bearer on enrollment
	// requests. Required to pass the API gateway that sits in front of the backend
	// receiver. The customer identity is derived by the backend from the SAK and
	// embedded in the returned JWT's assertion.customer_id claim.
	// Injected from a Kubernetes Secret via environment variable.
	SAKToken configopaque.String `mapstructure:"sak_token"`

	// TLS configures certificate verification for the enrollment endpoint.
	TLS configtls.ClientConfig `mapstructure:"tls"`
}

func (c *Config) Validate() error {
	if c.EnrollEndpoint == "" {
		return fmt.Errorf("sakauth: enroll_endpoint is required")
	}
	endpoint, err := url.Parse(c.EnrollEndpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return fmt.Errorf("sakauth: enroll_endpoint must be an absolute HTTPS URL")
	}
	if endpoint.User != nil || endpoint.Fragment != "" {
		return fmt.Errorf("sakauth: enroll_endpoint must not contain credentials or a fragment")
	}
	if c.SAKToken == "" {
		return fmt.Errorf("sakauth: sak_token is required")
	}
	if c.TLS.Insecure {
		return fmt.Errorf("sakauth: tls.insecure is not allowed; enroll_endpoint must use HTTPS")
	}
	// Disabling certificate verification keeps the https:// scheme but lets any
	// man-in-the-middle present its own certificate and capture both the SAK
	// sent on enrollment and the JWT returned by it.
	if c.TLS.InsecureSkipVerify {
		return fmt.Errorf("sakauth: tls.insecure_skip_verify is not allowed; the SAK and the enrollment JWT must not be exposed to an unverified peer")
	}
	return nil
}
