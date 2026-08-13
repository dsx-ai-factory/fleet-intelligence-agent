#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <dist-directory> <public-key.asc>" >&2
  exit 1
fi

dist_dir="$(cd "$1" && pwd)"
public_key="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
nvsec_bin="${NVSEC_BIN:-nvsec}"
job_type="${NVSEC_LINUX_PACKAGE_JOB_TYPE:-LINUX_PACKAGE_FLEET_INTELLIGENCE}"
scope="${NVSEC_LINUX_PACKAGE_SSA_SCOPE:-SIGNING_LINUX_PACKAGE_FLEET_INTELLIGENCE}"

: "${NVSEC_SSA_CLIENT_ID:?NVSEC_SSA_CLIENT_ID is required}"
: "${NVSEC_SSA_CLIENT_SECRET:?NVSEC_SSA_CLIENT_SECRET is required}"
command -v "$nvsec_bin" >/dev/null 2>&1 || { echo "nvsec is required" >&2; exit 1; }
[[ -s "$public_key" ]] || { echo "Public key not found or empty: $public_key" >&2; exit 1; }

shopt -s nullglob
packages=("$dist_dir"/*.deb "$dist_dir"/*.rpm)
shopt -u nullglob
(( ${#packages[@]} > 0 )) || {
  echo "No DEB or RPM packages found in $dist_dir" >&2
  exit 1
}

work_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/fleetint-package-sign.XXXXXX")"
cleanup() {
  case "$work_dir" in
    "${RUNNER_TEMP:-/tmp}"/fleetint-package-sign.*) rm -rf "$work_dir" ;;
    *) echo "Refusing to remove unexpected temporary directory: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT

for package in "${packages[@]}"; do
  package_name="$(basename "$package")"
  result_dir="$work_dir/$package_name"
  mkdir -p "$result_dir"

  args=(
    3s submit
    --job_type "$job_type"
    --description "fleetint ${GITHUB_REF_NAME:-snapshot} package signing: $package_name"
    --input_file "$package"
    --download
    --print_log
    --auth ssa
    --scope "$scope"
    --timeout 600
    --result_dir "$result_dir"
    --result_filename "$package_name"
  )
  [[ -z "${NSPECT_ID:-}" ]] || args+=(--nspect_id "$NSPECT_ID")

  echo "Signing $package_name with 3S job $job_type"
  "$nvsec_bin" "${args[@]}"

  signed_package="$result_dir/$package_name"
  [[ -s "$signed_package" ]] || {
    echo "3S did not return a signed package for $package_name" >&2
    find "$result_dir" -maxdepth 1 -name 'sign_r*.log' -type f -exec tail -n 100 {} \; >&2
    exit 1
  }

  "$(dirname "$0")/verify-linux-package-signature.sh" "$signed_package" "$public_key"

  replacement="$dist_dir/.$package_name.signed"
  install -m 0644 "$signed_package" "$replacement"
  mv "$replacement" "$package"
  echo "Replaced $package_name with its verified signed package"
done

echo "Signed and verified ${#packages[@]} Linux packages"
