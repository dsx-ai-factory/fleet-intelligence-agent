# Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Check release input boundaries and the deployable packaged NGC chart."""

import importlib.util
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("release_config", ROOT / "scripts/release-config.py")
RELEASE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(RELEASE)


class ReleaseConfigTests(unittest.TestCase):
    def test_staging_uses_agreed_names(self):
        config = RELEASE.configuration({"RELEASE_VERSION": "v1.2.3-rc.1", "NGC_ORG": "1234567890"})
        self.assertEqual(config["version"], "1.2.3-rc.1")
        self.assertEqual(config["image"], "nvcr.io/1234567890/fleet-intelligence/agent")
        self.assertEqual(config["gateway_image"], "nvcr.io/1234567890/fleet-intelligence/otel-gateway")
        self.assertEqual(config["chart_target"], "1234567890/fleet-intelligence/fleet-intelligence-agent")

    def test_versions_are_semver_and_safe_for_output_and_tags(self):
        for tag in ("1.2.3", "v01.2.3", "v1.2.3-01", "v1.2.3-", "v1.2", "v1.2.3\nimage=bad", "v1.2.3;echo bad", "v1.2.3+build"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                RELEASE.configuration({"RELEASE_VERSION": tag, "NGC_ORG": "1234567890"})
        self.assertEqual(RELEASE.configuration({"RELEASE_VERSION": "v0.2.0", "NGC_ORG": "1234567890"})["version"], "0.2.0")

    def test_missing_public_and_invalid_destinations_cannot_publish(self):
        for org in ("", "nvidia", "nvidia/team", "../nvidia", "org\nimage=bad"):
            with self.subTest(org=org), self.assertRaises(ValueError):
                RELEASE.configuration({"RELEASE_VERSION": "v1.2.3", "NGC_ORG": org})

    def test_missing_version_cannot_publish(self):
        with self.assertRaises(ValueError):
            RELEASE.configuration({"NGC_ORG": "1234567890"})


@unittest.skipUnless(shutil.which("helm"), "Helm is required for release chart checks")
class ReleaseChartTests(unittest.TestCase):
    def test_package_name_version_and_public_images(self):
        with tempfile.TemporaryDirectory() as directory:
            subprocess.run(["helm", "package", str(ROOT / "deployments/helm/fleet-intelligence-agent"), "--version", "1.2.3-rc.1", "--app-version", "1.2.3-rc.1", "--destination", directory], check=True, capture_output=True, text=True)
            package = Path(directory) / "fleet-intelligence-agent-1.2.3-rc.1.tgz"
            self.assertTrue(package.is_file())
            command = ["helm", "template", "fleet-intelligence-agent", str(package)]
            command += ["--set", "otelGateway.enabled=true", "--set", "otelGateway.enroll.existingSecret=test-token", "--set", "otelGateway.resources.limits.memory=256Mi", "--set", "otelGateway.backendEndpoint=https://api.example.com/otlp", "--set", "otelGateway.enroll.endpoint=https://api.example.com/enroll"]
            rendered = subprocess.run(command, check=True, capture_output=True, text=True).stdout
            self.assertIn("nvcr.io/nvidia/fleet-intelligence/agent:1.2.3-rc.1", rendered)
            self.assertIn("nvcr.io/nvidia/fleet-intelligence/otel-gateway:1.2.3-rc.1", rendered)
            self.assertNotIn("ghcr.io/", rendered)
            self.assertIn("name: fleet-intelligence-agent", rendered)


if __name__ == "__main__":
    unittest.main()
