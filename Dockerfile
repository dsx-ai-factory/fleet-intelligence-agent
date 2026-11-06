ARG DCGM_VERSION="4.7.0-ubuntu24.04@sha256:c7e042a939a5c0d4d588fff28f9371d9d101e66eece7b3f831cf5d44fa182fc8"

FROM golang:1.26.10 AS build

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    build-essential \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./
# Local replace target must exist before go mod download (replace => ./third_party/fleet-intelligence-sdk).
COPY third_party/fleet-intelligence-sdk ./third_party/fleet-intelligence-sdk

RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH
ARG BUILD_TIMESTAMP=""
ARG VERSION="0.0.1+container"
ARG REVISION=""

RUN CGO_ENABLED=1 GOOS=${TARGETOS} GOARCH=${TARGETARCH:-amd64} \
  go build -trimpath \
    -ldflags "-s -w \
      -X github.com/dsx-ai-factory/fleet-intelligence-agent/internal/version.BuildTimestamp=${BUILD_TIMESTAMP} \
      -X github.com/dsx-ai-factory/fleet-intelligence-agent/internal/version.Version=${VERSION} \
      -X github.com/dsx-ai-factory/fleet-intelligence-agent/internal/version.Revision=${REVISION} \
      -X github.com/dsx-ai-factory/fleet-intelligence-agent/internal/version.Package=github.com/dsx-ai-factory/fleet-intelligence-agent" \
    -o /out/fleetint ./cmd/fleetint

FROM nvcr.io/nvidia/cloud-native/dcgm:${DCGM_VERSION} AS runtime

ENV DEBIAN_FRONTEND=noninteractive
RUN set -eux; \
  apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    libssl3t64 \
    openssl \
    bash \
    sudo \
    kmod \
    util-linux \
    dmidecode \
    pciutils \
    wget \
    gnupg \
    gnupg2 \
  ; \
  arch="$(dpkg --print-architecture)"; \
  case "${arch}" in \
    amd64) cuda_arch="x86_64" ;; \
    arm64) cuda_arch="sbsa" ;; \
    *) echo "unsupported arch: ${arch}" >&2; exit 1 ;; \
  esac; \
  wget -qO /usr/share/keyrings/cuda-archive-keyring.gpg \
    "https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/${cuda_arch}/cuda-archive-keyring.gpg"; \
  echo "deb [signed-by=/usr/share/keyrings/cuda-archive-keyring.gpg] \
    https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/${cuda_arch}/ /" \
    > /etc/apt/sources.list.d/cuda.list; \
  apt-get update; \
  apt-get install -y --no-install-recommends \
    nvattest \
    corelib \
  ; \
  rm -rf /var/lib/apt/lists/*

COPY --from=build /out/fleetint /usr/bin/fleetint

EXPOSE 15133
VOLUME ["/var/lib/fleetint"]

ENTRYPOINT ["/usr/bin/fleetint"]
CMD ["run"]
