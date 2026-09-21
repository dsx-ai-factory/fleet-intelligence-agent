#!/usr/bin/env bash

set -euo pipefail
exec >/dev/null 2>&1

if [[ $# -ne 1 ]]; then
  exit 2
fi

dist_dir="${1%/}"

[[ -n "${ARTIFACTORY_URL:-}" ]] || exit 1
[[ -n "${ARTIFACTORY_USER:-}" ]] || exit 1
[[ -n "${ARTIFACTORY_TOKEN:-}" ]] || exit 1

artifactory_destination_url="${ARTIFACTORY_URL%/}"

[[ -d "$dist_dir" ]] || exit 1

[[ "$artifactory_destination_url" == https://* ]] || exit 1

[[ "$ARTIFACTORY_USER" != *[$'\r\n\t ']* ]] || exit 1
[[ "$ARTIFACTORY_TOKEN" != *[$'\r\n\t ']* ]] || exit 1

declare -a packages=()
for architecture in amd64 arm64; do
  shopt -s nullglob
  matches=("$dist_dir"/fleetint_*_"$architecture".deb)
  shopt -u nullglob

  [[ ${#matches[@]} -eq 1 ]] || exit 1
  [[ -s "${matches[0]}" ]] || exit 1
  packages+=("$architecture:${matches[0]}")
done

credentials_file="$(mktemp "${RUNNER_TEMP:-/tmp}/fleetint-artifactory-netrc.XXXXXX")"
cleanup() {
  case "$credentials_file" in
    "${RUNNER_TEMP:-/tmp}"/fleetint-artifactory-netrc.*) rm -f "$credentials_file" ;;
  esac
}
trap cleanup EXIT
chmod 0600 "$credentials_file"

artifactory_authority="${artifactory_destination_url#https://}"
artifactory_authority="${artifactory_authority%%/*}"
[[ "$artifactory_authority" != *"@"* ]] || exit 1
[[ "$artifactory_authority" != \[* ]] || exit 1
artifactory_host="${artifactory_authority%%:*}"
[[ -n "$artifactory_host" ]] || exit 1
printf 'machine %s\nlogin %s\npassword %s\n' \
  "$artifactory_host" \
  "$ARTIFACTORY_USER" \
  "$ARTIFACTORY_TOKEN" > "$credentials_file"

publish_packages() {
  local package_entry architecture package package_name upload_url

  for package_entry in "${packages[@]}"; do
    architecture="${package_entry%%:*}"
    package="${package_entry#*:}"
    package_name="$(basename "$package")"
    [[ "$package_name" =~ ^fleetint_[a-zA-Z0-9.+~_-]+_(amd64|arm64)\.deb$ ]] || return 1

    upload_url="${artifactory_destination_url}/${package_name}"
    upload_url+=";deb.distribution=noble;deb.component=main;deb.architecture=${architecture}"

    curl \
      --fail \
      --silent \
      --output /dev/null \
      --connect-timeout 15 \
      --max-time 600 \
      --low-speed-limit 1024 \
      --low-speed-time 60 \
      --retry 2 \
      --retry-delay 2 \
      --retry-max-time 300 \
      --retry-connrefused \
      --netrc-file "$credentials_file" \
      --upload-file "$package" \
      "$upload_url" || return 1
  done
}

for transaction_attempt in 1 2 3; do
  publish_packages && exit 0
  (( transaction_attempt == 3 )) || sleep $((transaction_attempt * 2))
done

exit 1
