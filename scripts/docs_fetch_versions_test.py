# SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0

import importlib.util
import io
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT_PATH = Path(__file__).with_name("docs-fetch-versions.py")
SPEC = importlib.util.spec_from_file_location("docs_fetch_versions", SCRIPT_PATH)
assert SPEC is not None
assert SPEC.loader is not None
docs_fetch_versions = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(docs_fetch_versions)


class FetchVersionTest(unittest.TestCase):
    def test_stages_release_page_and_adds_it_to_navigation(self) -> None:
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode="w"):
            pass

        root_pages = {
            "README.md": "# Overview\n",
            "RELEASE.md": "# Release Process\n",
            "CONTRIBUTING.md": (
                "# Contributing\n\n"
                "Follow the [release process](RELEASE.md) when publishing.\n"
            ),
        }
        git_archive = subprocess.CompletedProcess(
            args=["git", "archive"],
            returncode=0,
            stdout=archive.getvalue(),
        )

        with tempfile.TemporaryDirectory() as temp_dir:
            with (
                mock.patch.object(docs_fetch_versions, "VERSIONS_DIR", Path(temp_dir)),
                mock.patch.object(
                    docs_fetch_versions,
                    "get_git_file",
                    side_effect=lambda _tag, path: root_pages.get(path),
                ),
                mock.patch.object(docs_fetch_versions.subprocess, "run", return_value=git_archive),
            ):
                files = docs_fetch_versions.fetch_version("1.2", "v1.2.3")

            release_page = Path(temp_dir, "1.2-content", "release.md")
            self.assertEqual(
                release_page.read_text(encoding="utf-8"),
                "# Release Process\n",
            )
            self.assertEqual(
                files,
                ["overview.md", "release.md", "contributing.md"],
            )

            contributing_page = Path(temp_dir, "1.2-content", "contributing.md")
            contributing_content = contributing_page.read_text(encoding="utf-8")
            self.assertIn("[release process](release.md)", contributing_content)
            self.assertNotIn("[release process](RELEASE.md)", contributing_content)

            navigation = docs_fetch_versions.generate_nav_yml("1.2", files)
            self.assertIn("  - page: Release Process\n", navigation)
            self.assertIn("    path: 1.2-content/release.md\n", navigation)
            self.assertIn("    slug: release\n", navigation)


if __name__ == "__main__":
    unittest.main()
