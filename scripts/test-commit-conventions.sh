#!/bin/sh

# SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
set -eu

cd "$(dirname "$0")/.."
message_file=$(mktemp)
trap 'rm -f "$message_file"' EXIT HUP INT TERM

accept() {
  sh .git-hooks/commit-msg --subject "$1"
  printf '# comment\n\n%s\n\nCommit body.\n' "$1" > "$message_file"
  sh .git-hooks/commit-msg "$message_file"
}
reject() {
  if sh .git-hooks/commit-msg --subject "$1" >/dev/null 2>&1; then
    echo "Unexpectedly accepted title: $1" >&2
    exit 1
  fi
  printf '%s\n' "$1" > "$message_file"
  if sh .git-hooks/commit-msg "$message_file" >/dev/null 2>&1; then
    echo "Unexpectedly accepted commit: $1" >&2
    exit 1
  fi
}

for type in feat fix perf refactor docs chore test revert; do
  accept "$type: update behavior"
  accept "$type(cli): update behavior"
  accept "$type!: change behavior"
  accept "$type(api/v2)!: change behavior"
done
reject ''
reject 'add feature'
reject 'Feat: add feature'
reject 'build: update image'
reject 'fix(API): handle missing chassis'
reject 'fix:missing space'
reject 'fix: '
reject 'fix:  '
reject 'fix:  handle missing chassis'

# Local rebase/merge exceptions must never bypass strict PR-title validation.
for subject in 'Merge branch main' 'Revert "old change"' 'fixup! fix: handle missing chassis' 'squash! feat: add feature' 'amend! docs: update guide'; do
  printf '%s\n' "$subject" > "$message_file"
  sh .git-hooks/commit-msg "$message_file"
  if sh .git-hooks/commit-msg --subject "$subject" >/dev/null 2>&1; then
    echo "Unexpectedly accepted Git-generated PR title: $subject" >&2
    exit 1
  fi
done

: > "$message_file"
if sh .git-hooks/commit-msg "$message_file" >/dev/null 2>&1; then
  echo "Unexpectedly accepted empty commit." >&2
  exit 1
fi
if sh .git-hooks/commit-msg --subject 'fix: first line
fix: second line' >/dev/null 2>&1; then
  echo "Unexpectedly accepted multiline title." >&2
  exit 1
fi

# Shell metacharacters are data, including in titles supplied through CI env.
accept 'fix(cli): handle $HOME, `commands`, and $(commands) literally'
echo "Contribution convention checks passed."
