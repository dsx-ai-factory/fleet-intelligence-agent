#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

readonly EXPECTED_FINGERPRINT="FE0C8B74CA66357C13BE197DCCE3C963087199B3"

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <checksums-file> <public-key.asc>" >&2
  exit 1
fi

checksums_file="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
public_key="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
nvsec_bin="${NVSEC_BIN:-nvsec}"
job_type="${NVSEC_CHECKSUM_JOB_TYPE:-FLEET_INTELLIGENCE}"
scope="${NVSEC_CHECKSUM_SSA_SCOPE:-SIGNING_FLEET_INTELLIGENCE}"
signature_name="$(basename "$checksums_file").asc"

: "${NVSEC_SSA_CLIENT_ID:?NVSEC_SSA_CLIENT_ID is required}"
: "${NVSEC_SSA_CLIENT_SECRET:?NVSEC_SSA_CLIENT_SECRET is required}"
command -v "$nvsec_bin" >/dev/null 2>&1 || { echo "nvsec is required" >&2; exit 1; }
command -v gpg >/dev/null 2>&1 || { echo "gpg is required" >&2; exit 1; }
command -v gpgv >/dev/null 2>&1 || { echo "gpgv is required" >&2; exit 1; }
[[ -s "$checksums_file" ]] || { echo "Checksum file not found or empty: $checksums_file" >&2; exit 1; }
[[ -s "$public_key" ]] || { echo "Public key not found or empty: $public_key" >&2; exit 1; }

work_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/fleetint-checksum-sign.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT

args=(
  3s submit
  --job_type "$job_type"
  --description "Fleet Intelligence ${GITHUB_REF_NAME:-snapshot} checksum manifest"
  --input_file "$checksums_file"
  --download
  --print_log
  --auth ssa
  --scope "$scope"
  --timeout 600
  --result_dir "$work_dir"
  --result_filename "$signature_name"
)
[[ -z "${NSPECT_ID:-}" ]] || args+=(--nspect_id "$NSPECT_ID")

echo "Signing $(basename "$checksums_file") with 3S job $job_type"
"$nvsec_bin" "${args[@]}"

signature="$work_dir/$signature_name"
[[ -s "$signature" ]] || {
  echo "3S did not return a detached checksum signature" >&2
  find "$work_dir" -maxdepth 1 -name 'sign_r*.log' -type f -exec tail -n 100 {} \; >&2
  exit 1
}

key_home="$work_dir/key-home"
keyring="$work_dir/fleet-intelligence.gpg"
status_file="$work_dir/gpg-status"
mkdir -m 0700 "$key_home"

fingerprint="$(
  gpg --batch --homedir "$key_home" --show-keys --with-colons "$public_key" |
    awk -F: '$1 == "fpr" { print $10; exit }'
)"
[[ "$fingerprint" == "$EXPECTED_FINGERPRINT" ]] || {
  echo "Unexpected Fleet Intelligence key fingerprint: $fingerprint" >&2
  exit 1
}

gpg --batch --homedir "$key_home" --yes --dearmor --output "$keyring" "$public_key"
gpgv --status-fd 1 --keyring "$keyring" "$signature" "$checksums_file" > "$status_file" 2>&1
grep -q "^\[GNUPG:\] VALIDSIG $EXPECTED_FINGERPRINT " "$status_file" || {
  echo "Checksum signature was not made by the expected Fleet Intelligence key" >&2
  cat "$status_file" >&2
  exit 1
}

install -m 0644 "$signature" "$checksums_file.asc"
echo "Created verified detached signature $checksums_file.asc"
