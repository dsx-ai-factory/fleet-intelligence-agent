# Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

"""Exercise exact-source provenance, immutable copies, and publication failures."""

import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("mirror", Path(__file__).with_name("mirror-ngc-release.py"))
MIRROR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MIRROR)
DIGEST = "sha256:" + "a" * 64
OTHER_DIGEST = "sha256:" + "b" * 64


class MirrorTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.environ = {"RELEASE_TAG": "v1.2.3", "GITHUB_SHA": "a" * 40,
                        "GITHUB_REPOSITORY": "dsx-ai-factory/fleet-intelligence-agent"}
        self.calls = []
        self.copy_digest = DIGEST

    def command(self, *args):
        self.calls.append(args)
        if args[:2] == ("crane", "digest"):
            return self.copy_digest if args[2].startswith("ghcr.io/") else DIGEST
        if args[:2] == ("helm", "pull"):
            version = args[args.index("--version") + 1]
            (self.directory / f"fleet-intelligence-agent-{version}.tgz").write_bytes(b"approved chart package")
        return "[]"

    def prepare(self):
        with patch.object(MIRROR, "run", side_effect=self.command):
            MIRROR.prepare(self.directory, self.environ)

    def publish(self):
        with patch.object(MIRROR, "run", side_effect=self.command):
            MIRROR.publish(self.directory, self.environ)

    def test_verify_exact_commit_and_copy_without_rebuild(self):
        self.prepare()
        verification = [call for call in self.calls if call[:3] == ("gh", "attestation", "verify")]
        self.assertEqual(len(verification), 3)
        for call in verification:
            self.assertEqual(call[call.index("--source-digest") + 1], self.environ["GITHUB_SHA"])
            self.assertEqual(call[call.index("--signer-workflow") + 1], self.environ["GITHUB_REPOSITORY"] + "/.github/workflows/stage-ngc-release.yml")
        chart = self.directory / "fleet-intelligence-agent-1.2.3.tgz"
        before = MIRROR.checksum(chart)
        self.publish()
        creates = [call for call in self.calls if call[:2] == ("crane", "copy")]
        self.assertEqual(len(creates), 2)
        self.assertTrue(all(call[-2].endswith("@" + DIGEST) for call in creates))
        self.assertTrue(all("--no-clobber" in call for call in creates))
        latest = [call for call in self.calls if call[:2] == ("crane", "tag")]
        self.assertEqual(len(latest), 2)
        self.assertTrue(all(call[-1] == "latest" for call in latest))
        self.assertEqual(self.calls[-1], ("helm", "push", str(chart), "oci://ghcr.io/dsx-ai-factory/charts"))
        self.assertEqual(MIRROR.checksum(chart), before)
        self.assertFalse(any(call[:2] == ("docker", "build") for call in self.calls))

    def test_provenance_failure_blocks_prepare_and_all_publication(self):
        def failed(*args):
            if args[:3] == ("gh", "attestation", "verify"):
                raise subprocess.CalledProcessError(1, args)
            return self.command(*args)
        with patch.object(MIRROR, "run", side_effect=failed), self.assertRaises(subprocess.CalledProcessError):
            MIRROR.prepare(self.directory, self.environ)
        self.assertFalse((self.directory / "release-record.json").exists())
        self.assertFalse(any(call[:2] == ("helm", "push") for call in self.calls))

    def test_chart_provenance_failure_does_not_create_approved_record(self):
        def failed(*args):
            if args[:3] == ("gh", "attestation", "verify") and args[3].endswith(".tgz"):
                raise subprocess.CalledProcessError(1, args)
            return self.command(*args)
        with patch.object(MIRROR, "run", side_effect=failed), self.assertRaises(subprocess.CalledProcessError):
            MIRROR.prepare(self.directory, self.environ)
        self.assertFalse((self.directory / "release-record.json").exists())

    def test_changed_chart_cannot_publish(self):
        self.prepare()
        (self.directory / "fleet-intelligence-agent-1.2.3.tgz").write_bytes(b"changed chart")
        self.calls.clear()
        with self.assertRaisesRegex(ValueError, "chart bytes changed"):
            self.publish()
        self.assertEqual(self.calls, [])

    def test_record_from_another_source_cannot_publish(self):
        self.prepare()
        self.environ["GITHUB_SHA"] = "b" * 40
        self.calls.clear()
        with self.assertRaisesRegex(ValueError, "does not match"):
            self.publish()
        self.assertEqual(self.calls, [])

    def test_copy_digest_mismatch_stops_before_chart_upload(self):
        self.prepare()
        self.copy_digest = OTHER_DIGEST
        with self.assertRaisesRegex(ValueError, "changed the approved"):
            self.publish()
        self.assertFalse(any(call[:2] == ("helm", "push") for call in self.calls))

    def test_prerelease_does_not_update_latest(self):
        self.environ["RELEASE_TAG"] = "v1.2.3-rc.1"
        self.prepare()
        self.publish()
        self.assertFalse(any(call[:2] == ("crane", "tag") for call in self.calls))

    def test_invalid_identity_rejected_before_commands(self):
        self.environ["RELEASE_TAG"] = "v1.2.3\nimage=bad"
        with patch.object(MIRROR, "run", side_effect=self.command), self.assertRaises(ValueError):
            MIRROR.prepare(self.directory, self.environ)
        self.assertEqual(self.calls, [])


if __name__ == "__main__":
    unittest.main()
