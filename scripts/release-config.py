#!/usr/bin/env python3
# Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Validate a release version and private NGC destination before publishing."""

import os
import re
import sys

NUMBER = r"(?:0|[1-9][0-9]*)"
IDENTIFIER = rf"(?:{NUMBER}|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
VERSION = rf"v({NUMBER}\.{NUMBER}\.{NUMBER}(?:-{IDENTIFIER}(?:\.{IDENTIFIER})*)?)"


def configuration(environ):
    """Return safe workflow outputs; public NGC is never a CI upload target."""
    tag = environ.get("RELEASE_VERSION", "")
    match = re.fullmatch(VERSION, tag)
    if not match:
        raise ValueError("Release version must be vMAJOR.MINOR.PATCH, optionally with a prerelease")
    org = environ.get("NGC_ORG", "")
    if not re.fullmatch(r"[a-z0-9]+(?:[._-][a-z0-9]+)*", org) or org == "nvidia":
        raise ValueError("NGC_ORG must name the private staging organization, not public nvidia")
    prefix = f"nvcr.io/{org}/fleet-intelligence"
    return {
        "tag": tag,
        "version": match.group(1),
        "image": f"{prefix}/agent",
        "gateway_image": f"{prefix}/otel-gateway",
        "chart_target": f"{org}/fleet-intelligence/fleet-intelligence-agent",
    }


def main():
    try:
        outputs = configuration(os.environ)
    except ValueError as error:
        print(f"::error::{error}", file=sys.stderr)
        return 1
    with open(os.environ["GITHUB_OUTPUT"], "a") as output:
        output.writelines(f"{key}={value}\n" for key, value in outputs.items())
    return 0


if __name__ == "__main__":
    sys.exit(main())
