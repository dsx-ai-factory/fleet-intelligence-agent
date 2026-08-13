#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <checksums-file> [extra-artifact ...]" >&2
  exit 1
fi

checksum_file="$1"
dist_dir="$(cd "$(dirname "$checksum_file")" && pwd)"
checksum_file="$dist_dir/$(basename "$checksum_file")"

[[ -s "$checksum_file" ]] || {
  echo "Checksum file not found or empty: $checksum_file" >&2
  exit 1
}

if command -v sha256sum >/dev/null 2>&1; then
  sha256_file() {
    sha256sum "$1" | awk '{print $1}'
  }
elif command -v shasum >/dev/null 2>&1; then
  sha256_file() {
    shasum -a 256 "$1" | awk '{print $1}'
  }
else
  echo "sha256sum or shasum is required" >&2
  exit 1
fi

updated_file="$(mktemp "$dist_dir/.checksums.txt.XXXXXX")"
cleanup() {
  rm -f "$updated_file"
}
trap cleanup EXIT

artifact_count=0
refreshed_count=0
verified_count=0
while read -r expected_hash artifact_name extra || [[ -n "${artifact_name:-}" ]]; do
  [[ -z "${extra:-}" ]] || {
    echo "Unsupported checksum line for an artifact containing whitespace: $artifact_name $extra" >&2
    exit 1
  }
  [[ "$expected_hash" =~ ^[[:xdigit:]]{64}$ ]] || {
    echo "Invalid SHA-256 checksum for $artifact_name: $expected_hash" >&2
    exit 1
  }
  artifact_name="${artifact_name#\*}"
  [[ "$artifact_name" != */* && -n "$artifact_name" ]] || {
    echo "Unsafe artifact name in checksum file: $artifact_name" >&2
    exit 1
  }

  artifact_path="$dist_dir/$artifact_name"
  [[ -f "$artifact_path" ]] || {
    echo "Artifact listed in checksum file is missing: $artifact_path" >&2
    exit 1
  }

  actual_hash="$(sha256_file "$artifact_path")"
  expected_hash_lc="$(printf '%s' "$expected_hash" | tr '[:upper:]' '[:lower:]')"
  case "$artifact_name" in
    *.deb|*.rpm)
      printf '%s  %s\n' "$actual_hash" "$artifact_name" >> "$updated_file"
      refreshed_count=$((refreshed_count + 1))
      ;;
    *)
      [[ "$actual_hash" == "$expected_hash_lc" ]] || {
        echo "Unexpected checksum change for unsigned artifact: $artifact_name" >&2
        exit 1
      }
      printf '%s  %s\n' "$expected_hash" "$artifact_name" >> "$updated_file"
      verified_count=$((verified_count + 1))
      ;;
  esac
  artifact_count=$((artifact_count + 1))
done < "$checksum_file"

for extra_artifact in "${@:2}"; do
  [[ -f "$extra_artifact" ]] || {
    echo "Extra checksum artifact is missing: $extra_artifact" >&2
    exit 1
  }
  artifact_name="$(basename "$extra_artifact")"
  [[ "$artifact_name" != *" "* ]] || {
    echo "Extra artifact name contains unsupported whitespace: $artifact_name" >&2
    exit 1
  }
  if awk '{ print $2 }' "$updated_file" | grep -Fxq "$artifact_name"; then
    continue
  fi
  printf '%s  %s\n' "$(sha256_file "$extra_artifact")" "$artifact_name" >> "$updated_file"
  artifact_count=$((artifact_count + 1))
done

(( artifact_count > 0 )) || {
  echo "No artifacts were found in $checksum_file" >&2
  exit 1
}

chmod 0644 "$updated_file"
mv "$updated_file" "$checksum_file"
trap - EXIT

echo "Refreshed $refreshed_count signed package checksums and verified $verified_count unchanged release artifacts"
