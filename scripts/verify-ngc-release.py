#!/usr/bin/env python3
# Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Verify public NGC release artifacts against their exact approved source."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import runpy
import subprocess
import sys

PUBLIC_PREFIX = "nvcr.io/nvidia/fleet-intelligence"
CHART_REPO = "https://helm.ngc.nvidia.com/nvidia/fleet-intelligence"
IMAGES = ("agent", "otel-gateway")


def run(*args):
    """Run a checked command without a shell, returning its stdout."""
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def context(environ):
    """Validate the release identity before using it in registry references."""
    pattern = runpy.run_path(str(Path(__file__).with_name("release-config.py")))["VERSION"]
    match = re.fullmatch(pattern, environ.get("RELEASE_TAG", ""))
    sha = environ.get("GITHUB_SHA", "")
    repository = environ.get("GITHUB_REPOSITORY", "")
    if not match or not re.fullmatch(r"[0-9a-f]{40}", sha):
        raise ValueError("A valid release tag and exact source commit are required")
    if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository):
        raise ValueError("GITHUB_REPOSITORY must identify the release repository")
    return match.group(1), sha, repository


def verified_digest(value):
    if not isinstance(value, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", value):
        raise ValueError("Registry returned an invalid image digest")
    return value


def checksum(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify(directory, environ):
    """Require provenance for the tag's exact source before package publication."""
    version, sha, repository = context(environ)
    directory.mkdir(parents=True, exist_ok=True)
    policy = ("--repo", repository, "--source-digest", sha,
              "--signer-workflow", f"{repository}/.github/workflows/stage-ngc-release.yml")
    images = {}
    for image in IMAGES:
        reference = f"{PUBLIC_PREFIX}/{image}"
        digest = verified_digest(run("crane", "digest", f"{reference}:{version}").strip())
        result = run("gh", "attestation", "verify", f"oci://{reference}@{digest}",
                     "--bundle-from-oci", *policy, "--format", "json")
        (directory / f"{image}.verification.json").write_text(result)
        images[image] = digest
    run("helm", "pull", "fleet-intelligence-agent", "--repo", CHART_REPO,
        "--version", version, "--destination", str(directory))
    chart = directory / f"fleet-intelligence-agent-{version}.tgz"
    result = run("gh", "attestation", "verify", str(chart), *policy, "--format", "json")
    (directory / "chart.verification.json").write_text(result)
    record = {"version": version, "source_sha": sha, "repository": repository,
              "images": images, "chart_sha256": checksum(chart)}
    (directory / "release-record.json").write_text(json.dumps(record, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    try:
        verify(args.directory, os.environ)
    except (ValueError, KeyError, OSError, subprocess.CalledProcessError) as error:
        print(f"::error::NGC release verification failed: {error}", file=sys.stderr)
        if isinstance(error, subprocess.CalledProcessError) and error.stderr:
            print(error.stderr, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
