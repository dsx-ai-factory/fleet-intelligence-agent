#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

readonly EXPECTED_FINGERPRINT="FE0C8B74CA66357C13BE197DCCE3C963087199B3"

if [[ $# -ne 2 ]]; then
  echo "Usage: $0 <package.deb|package.rpm> <public-key.asc>" >&2
  exit 1
fi

package="$1"
public_key="$2"

[[ -s "$package" ]] || { echo "Package not found or empty: $package" >&2; exit 1; }
[[ -s "$public_key" ]] || { echo "Public key not found or empty: $public_key" >&2; exit 1; }

work_dir="$(mktemp -d "${RUNNER_TEMP:-/tmp}/fleetint-package-verify.XXXXXX")"
cleanup() {
  case "$work_dir" in
    "${RUNNER_TEMP:-/tmp}"/fleetint-package-verify.*) rm -rf "$work_dir" ;;
    *) echo "Refusing to remove unexpected temporary directory: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT

verify_key_fingerprint() {
  command -v gpg >/dev/null 2>&1 || { echo "gpg is required" >&2; exit 1; }

  local keycheck_home="$work_dir/keycheck"
  local actual_fingerprint
  mkdir -m 0700 "$keycheck_home"
  actual_fingerprint="$(
    gpg --batch --homedir "$keycheck_home" --show-keys --with-colons "$public_key" |
      awk -F: '$1 == "fpr" { print $10; exit }'
  )"
  [[ "$actual_fingerprint" == "$EXPECTED_FINGERPRINT" ]] || {
    echo "Unexpected Fleet Intelligence signing-key fingerprint: $actual_fingerprint" >&2
    exit 1
  }
}

verify_deb() {
  for command in ar awk gpg md5sum sha1sum; do
    command -v "$command" >/dev/null 2>&1 || {
      echo "$command is required to verify DEB signatures" >&2
      exit 1
    }
  done

  local gpg_home="$work_dir/gnupg"
  local signature_file="$work_dir/_gpgbuilder"
  local status_file="$work_dir/gpg-status"
  local manifest_file="$work_dir/manifest"
  mkdir -m 0700 "$gpg_home"

  ar p "$package" _gpgbuilder > "$signature_file"
  gpg --batch --homedir "$gpg_home" --quiet --import "$public_key"
  gpg --batch --homedir "$gpg_home" --status-fd 1 --verify "$signature_file" > "$status_file" 2>&1
  grep -q "^\\[GNUPG:\\] VALIDSIG $EXPECTED_FINGERPRINT " "$status_file" || {
    echo "DEB signature was not made by the expected Fleet Intelligence key" >&2
    cat "$status_file" >&2
    exit 1
  }

  awk '
    /^Files:[[:space:]]*$/ { in_files = 1; next }
    /^-----BEGIN PGP SIGNATURE-----$/ { in_files = 0 }
    in_files && NF == 4 { print $1, $2, $3, $4 }
  ' "$signature_file" > "$manifest_file"
  [[ -s "$manifest_file" ]] || {
    echo "DEB signature does not contain a package-member manifest" >&2
    exit 1
  }

  require_signed_member() {
    local member_pattern="$1"
    local member_description="$2"
    awk -v pattern="$member_pattern" '$4 ~ pattern { found = 1 } END { exit !found }' "$manifest_file" || {
      echo "DEB signature does not cover required member: $member_description" >&2
      exit 1
    }
  }
  require_signed_member '^debian-binary$' 'debian-binary'
  require_signed_member '^control[.]tar([.](gz|xz|zst|bz2|lzma))?$' 'control.tar.*'
  require_signed_member '^data[.]tar([.](gz|xz|zst|bz2|lzma))?$' 'data.tar.*'

  while read -r expected_md5 expected_sha1 expected_size member_name; do
    [[ "$member_name" != */* && "$member_name" != "_gpgbuilder" ]] || {
      echo "Unsafe DEB member in signed manifest: $member_name" >&2
      exit 1
    }

    local member_file="$work_dir/member"
    ar p "$package" "$member_name" > "$member_file"
    local actual_md5 actual_sha1 actual_size
    actual_md5="$(md5sum "$member_file" | awk '{print $1}')"
    actual_sha1="$(sha1sum "$member_file" | awk '{print $1}')"
    actual_size="$(wc -c < "$member_file" | tr -d '[:space:]')"

    [[ "$actual_md5" == "$expected_md5" ]] || {
      echo "DEB member MD5 mismatch: $member_name" >&2
      exit 1
    }
    [[ "$actual_sha1" == "$expected_sha1" ]] || {
      echo "DEB member SHA-1 mismatch: $member_name" >&2
      exit 1
    }
    [[ "$actual_size" == "$expected_size" ]] || {
      echo "DEB member size mismatch: $member_name" >&2
      exit 1
    }
  done < "$manifest_file"
}

verify_rpm() {
  command -v rpm >/dev/null 2>&1 || {
    echo "rpm is required to verify RPM signatures" >&2
    exit 1
  }

  local rpm_db="$work_dir/rpmdb"
  local rpm_status="$work_dir/rpm-status"
  mkdir -p "$rpm_db"
  rpm --dbpath "$rpm_db" --initdb
  rpm --dbpath "$rpm_db" --import "$public_key"
  rpm --dbpath "$rpm_db" --checksig --verbose "$package" | tee "$rpm_status"

  grep -Eiq "Signature, key ID 087199b3: OK" "$rpm_status" || {
    echo "RPM signature was not made by the expected Fleet Intelligence key" >&2
    exit 1
  }
  if grep -Eiq "NOT OK|NOKEY|NOTTRUSTED|BAD" "$rpm_status"; then
    echo "RPM signature or digest validation failed" >&2
    exit 1
  fi
}

verify_key_fingerprint
case "$package" in
  *.deb) verify_deb ;;
  *.rpm) verify_rpm ;;
  *) echo "Unsupported package format: $package" >&2; exit 1 ;;
esac

echo "Verified $(basename "$package") with Fleet Intelligence key $EXPECTED_FINGERPRINT"
