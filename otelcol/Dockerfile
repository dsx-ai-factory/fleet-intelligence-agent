# SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
#
# Builds the fleetint-otelcol custom collector binary using the OTel Collector Builder (ocb).
#
# Build from the repository root:
#   docker build -f otelcol/Dockerfile -t fleetint-otelcol:latest .

FROM golang:1.26.6 AS builder

# Install the OTel Collector Builder at the same version as our components.
RUN go install go.opentelemetry.io/collector/cmd/builder@v0.156.0
ENV CGO_ENABLED=0
ENV GOOS=linux

WORKDIR /src
# Copy only the collector sources required by the local module replacement.
COPY otelcol/otelcol-builder.yaml ./otelcol/otelcol-builder.yaml
COPY otelcol/auth/sakauth/ ./otelcol/auth/sakauth/

WORKDIR /src/otelcol
RUN builder --config=otelcol-builder.yaml --skip-compilation=false
RUN cp bin/fleetint-otelcol /bin/fleetint-otelcol

# ---

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /bin/fleetint-otelcol /fleetint-otelcol

EXPOSE 4318 13133

ENTRYPOINT ["/fleetint-otelcol"]
