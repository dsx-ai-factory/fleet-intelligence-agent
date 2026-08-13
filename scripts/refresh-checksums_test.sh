#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

script_dir="$(cd "$(dirname "$0")" && pwd)"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/fleetint-refresh-checksums-test.XXXXXX")"
trap 'rm -rf "$work_dir"' EXIT

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

printf 'original archive\n' > "$work_dir/fleetint.tar.gz"
printf 'unsigned package\n' > "$work_dir/fleetint.deb"
printf '%s  %s\n' "$(sha256_file "$work_dir/fleetint.tar.gz")" "fleetint.tar.gz" > "$work_dir/checksums.txt"
printf '%s  %s\n' "$(sha256_file "$work_dir/fleetint.deb")" "fleetint.deb" >> "$work_dir/checksums.txt"

printf 'signed package\n' > "$work_dir/fleetint.deb"
"$script_dir/refresh-checksums.sh" "$work_dir/checksums.txt"

grep -Fxq "$(sha256_file "$work_dir/fleetint.tar.gz")  fleetint.tar.gz" "$work_dir/checksums.txt"
grep -Fxq "$(sha256_file "$work_dir/fleetint.deb")  fleetint.deb" "$work_dir/checksums.txt"

cp "$work_dir/checksums.txt" "$work_dir/checksums.before-tamper"
printf 'tampered archive\n' > "$work_dir/fleetint.tar.gz"
if "$script_dir/refresh-checksums.sh" "$work_dir/checksums.txt"; then
  echo "Expected an unsigned artifact checksum change to fail" >&2
  exit 1
fi
cmp "$work_dir/checksums.before-tamper" "$work_dir/checksums.txt"

echo "refresh-checksums tests passed"
