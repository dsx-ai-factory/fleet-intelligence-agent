# Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Exercise exact-source provenance and verification failures without publishing."""

import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("verify", Path(__file__).with_name("verify-ngc-release.py"))
VERIFY = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VERIFY)
DIGEST = "sha256:" + "a" * 64


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.environ = {"RELEASE_TAG": "v1.2.3", "GITHUB_SHA": "a" * 40,
                        "GITHUB_REPOSITORY": "dsx-ai-factory/fleet-intelligence-agent"}
        self.calls = []

    def command(self, *args):
        self.calls.append(args)
        if args[:2] == ("crane", "digest"):
            return DIGEST
        if args[:2] == ("helm", "pull"):
            version = args[args.index("--version") + 1]
            (self.directory / f"fleet-intelligence-agent-{version}.tgz").write_bytes(b"approved chart package")
        return "[]"

    def test_verifies_both_images_and_chart_for_exact_source(self):
        with patch.object(VERIFY, "run", side_effect=self.command):
            VERIFY.verify(self.directory, self.environ)
        calls = [call for call in self.calls if call[:3] == ("gh", "attestation", "verify")]
        self.assertEqual(len(calls), 3)
        for call in calls:
            self.assertEqual(call[call.index("--source-digest") + 1], self.environ["GITHUB_SHA"])
            self.assertEqual(call[call.index("--signer-workflow") + 1], self.environ["GITHUB_REPOSITORY"] + "/.github/workflows/stage-ngc-release.yml")
        for call in calls[:2]:
            self.assertTrue(call[3].endswith("@" + DIGEST))
            self.assertIn("--bundle-from-oci", call)
        record = json.loads((self.directory / "release-record.json").read_text())
        self.assertEqual(record["images"], {"agent": DIGEST, "otel-gateway": DIGEST})
        self.assertEqual(record["source_sha"], self.environ["GITHUB_SHA"])
        self.assertEqual(record["chart_sha256"], VERIFY.checksum(self.directory / "fleet-intelligence-agent-1.2.3.tgz"))
        self.assertTrue(all(call[:2] in (("crane", "digest"), ("gh", "attestation"), ("helm", "pull")) for call in self.calls))

    def test_image_provenance_failure_blocks_approved_record(self):
        def failed(*args):
            if args[:3] == ("gh", "attestation", "verify"):
                raise subprocess.CalledProcessError(1, args)
            return self.command(*args)
        with patch.object(VERIFY, "run", side_effect=failed), self.assertRaises(subprocess.CalledProcessError):
            VERIFY.verify(self.directory, self.environ)
        self.assertFalse((self.directory / "release-record.json").exists())

    def test_chart_provenance_failure_blocks_approved_record(self):
        def failed(*args):
            if args[:3] == ("gh", "attestation", "verify") and args[3].endswith(".tgz"):
                raise subprocess.CalledProcessError(1, args)
            return self.command(*args)
        with patch.object(VERIFY, "run", side_effect=failed), self.assertRaises(subprocess.CalledProcessError):
            VERIFY.verify(self.directory, self.environ)
        self.assertFalse((self.directory / "release-record.json").exists())

    def test_invalid_identity_rejected_before_commands(self):
        for key, value in (("RELEASE_TAG", "v1.2.3\nimage=bad"), ("GITHUB_SHA", "not-a-commit"), ("GITHUB_REPOSITORY", "invalid")):
            with self.subTest(key=key), patch.object(VERIFY, "run", side_effect=self.command), self.assertRaises(ValueError):
                VERIFY.verify(self.directory, {**self.environ, key: value})
        self.assertEqual(self.calls, [])

    def test_invalid_registry_digest_rejected(self):
        with patch.object(VERIFY, "run", return_value="invalid-digest"), self.assertRaisesRegex(ValueError, "invalid image digest"):
            VERIFY.verify(self.directory, self.environ)
        self.assertFalse((self.directory / "release-record.json").exists())

    def test_prerelease_chart_version_matches_release(self):
        self.environ["RELEASE_TAG"] = "v1.2.3-rc.1"
        with patch.object(VERIFY, "run", side_effect=self.command):
            VERIFY.verify(self.directory, self.environ)
        record = json.loads((self.directory / "release-record.json").read_text())
        self.assertEqual(record["version"], "1.2.3-rc.1")


if __name__ == "__main__":
    unittest.main()
