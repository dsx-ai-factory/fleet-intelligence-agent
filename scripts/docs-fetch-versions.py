#!/usr/bin/env python3
# SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
"""
Fetch versioned agent docs from git tags for Fern local preview and CI publishing.

Reads fern/kept-versions.json to determine how many major.minor version groups
to keep, discovers the latest patch tag per group, and stages each version's
docs under fern/versions/<major.minor>-content/. Generates a Fern nav YML for
each version and writes fern/versions/manifest.json for the publish workflow.

Usage:
    # Fetch version content, preview HEAD docs only (no version selector):
    python3 scripts/docs-fetch-versions.py
    fern docs dev

    # Fetch version content and enable the version selector in local preview:
    python3 scripts/docs-fetch-versions.py --patch-fern-docs
    fern docs dev
    git checkout fern/docs.yml   # restore when done

    # Print discovered versions without fetching:
    python3 scripts/docs-fetch-versions.py --list
"""

import argparse
import io
import json
import re
import shutil
import subprocess
import sys
import tarfile
from collections import defaultdict
from pathlib import Path

REPO_ROOT = Path(__file__).parent.parent
FERN_DIR = REPO_ROOT / "fern"
VERSIONS_DIR = FERN_DIR / "versions"

SPDX = (
    "# SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES."
    " All rights reserved.\n"
    "# SPDX-License-Identifier: Apache-2.0\n\n"
)

# Human-readable page titles. Files not listed here fall back to title-casing
# the filename stem (e.g. "troubleshooting.md" → "Troubleshooting").
TITLE_MAP: dict[str, str] = {
    "architecture.md": "Architecture",
    "usage.md": "Usage",
    "configuration.md": "Configuration",
    "install-deb.md": "DEB Installation",
    "install-rpm.md": "RPM Installation",
    "install-helm.md": "Helm Installation",
    "development.md": "Development",
}

# Preferred page order. Files absent from this list are appended alphabetically.
PAGE_ORDER = [
    "architecture.md",
    "usage.md",
    "configuration.md",
    "install-deb.md",
    "install-rpm.md",
    "install-helm.md",
    "development.md",
]


def get_title(filename: str) -> str:
    if filename in TITLE_MAP:
        return TITLE_MAP[filename]
    return filename.removesuffix(".md").replace("-", " ").title()


def sort_files(files: list[str]) -> list[str]:
    ordered = [f for f in PAGE_ORDER if f in files]
    remaining = sorted(f for f in files if f not in PAGE_ORDER)
    return ordered + remaining


def fix_mdx(content: str) -> str:
    """Apply MDX safety fixes to markdown content, skipping fenced code blocks."""
    lines = content.split("\n")
    in_code_block = False
    result = []
    for line in lines:
        if re.match(r"^\s*```", line):
            in_code_block = not in_code_block
        if not in_code_block:
            # <https://...> autolinks → [url](url)
            line = re.sub(
                r"<(https://[^\s>]+)>",
                lambda m: f"[{m.group(1)}]({m.group(1)})",
                line,
            )
            # Bare <digit → &lt;digit (e.g. <500MB, <1%)
            line = re.sub(r"<(\d)", r"&lt;\1", line)
            # Cross-directory CONTRIBUTING link
            line = line.replace(
                "../CONTRIBUTING.md",
                "https://github.com/NVIDIA/fleet-intelligence-agent/blob/main/CONTRIBUTING.md",
            )
        result.append(line)
    return "\n".join(result)


def discover_versions(keep: int) -> list[tuple[str, str]]:
    """
    Return [(major.minor, tag), ...] for the top `keep` major.minor groups,
    each pinned to their highest patch release tag (pre-releases excluded).
    """
    result = subprocess.run(
        ["git", "tag", "--list", "v[0-9]*.[0-9]*.[0-9]*"],
        capture_output=True,
        text=True,
        cwd=REPO_ROOT,
        check=True,
    )
    groups: dict[tuple[int, int], list[tuple[int, str]]] = defaultdict(list)
    for tag in result.stdout.splitlines():
        tag = tag.strip()
        m = re.match(r"^v(\d+)\.(\d+)\.(\d+)$", tag)  # no pre-release suffix
        if m:
            major, minor, patch = int(m.group(1)), int(m.group(2)), int(m.group(3))
            groups[(major, minor)].append((patch, tag))

    best: dict[tuple[int, int], str] = {}
    for (major, minor), patches in groups.items():
        patches.sort(reverse=True)
        best[(major, minor)] = patches[0][1]

    top_groups = sorted(best.keys(), reverse=True)[:keep]
    return [(f"{maj}.{min_}", best[(maj, min_)]) for maj, min_ in top_groups]


def fetch_version(version_label: str, tag: str) -> list[str]:
    """
    Extract docs from a git tag into fern/versions/<version_label>-content/.
    Applies MDX safety fixes to all .md files.
    Returns the sorted list of .md filenames fetched.
    """
    content_dir = VERSIONS_DIR / f"{version_label}-content"
    if content_dir.exists():
        shutil.rmtree(content_dir)
    content_dir.mkdir(parents=True)

    archive_result = subprocess.run(
        ["git", "archive", tag, "--", "docs/"],
        capture_output=True,
        cwd=REPO_ROOT,
        check=True,
    )
    with tarfile.open(fileobj=io.BytesIO(archive_result.stdout)) as tar:
        tar.extractall(content_dir)

    # git archive preserves the docs/ prefix — flatten it
    docs_subdir = content_dir / "docs"
    md_files: list[str] = []
    if docs_subdir.is_dir():
        for src in docs_subdir.iterdir():
            if src.suffix == ".md":
                dest = content_dir / src.name
                dest.write_text(fix_mdx(src.read_text()))
                md_files.append(src.name)
        shutil.rmtree(docs_subdir)

    return sort_files(md_files)


def generate_nav_yml(version_label: str, md_files: list[str]) -> str:
    """Generate a Fern navigation YML for a version's staged content."""
    content_prefix = f"{version_label}-content"
    lines = [SPDX + "navigation:"]
    for filename in md_files:
        title = get_title(filename)
        slug = filename.removesuffix(".md")
        lines.append(f"  - page: {title}")
        lines.append(f"    path: {content_prefix}/{filename}")
        lines.append(f"    slug: {slug}")
    return "\n".join(lines) + "\n"


def patch_fern_docs_yml(manifest: list[dict]) -> None:
    """
    Replace the navigation: block in fern/docs.yml with a versions: block
    built from the fetched manifest, enabling the version selector in
    `fern docs dev`. Modifies fern/docs.yml in place — restore with:
        git checkout fern/docs.yml
    """
    docs_yml = FERN_DIR / "docs.yml"
    versions_lines = ["versions:"]
    for v in manifest:
        versions_lines += [
            f'  - display-name: "{v["display_name"]}"',
            f'    path: {v["nav_path"]}',
            f'    slug: "{v["slug"]}"',
        ]
    versions_block = "\n".join(versions_lines) + "\n"
    content = docs_yml.read_text()
    patched = re.sub(
        r"^navigation:(\n  .*)*\n",
        versions_block,
        content,
        flags=re.MULTILINE,
    )
    docs_yml.write_text(patched)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument(
        "--list",
        action="store_true",
        help="Print the versions that would be fetched without fetching them.",
    )
    parser.add_argument(
        "--patch-fern-docs",
        action="store_true",
        help=(
            "After fetching, patch fern/docs.yml to use a versions: block so "
            "the version selector appears in `fern docs dev`. "
            "Restore with: git checkout fern/docs.yml"
        ),
    )
    args = parser.parse_args()

    config = json.loads((FERN_DIR / "kept-versions.json").read_text())
    keep: int = config["keep"]

    versions = discover_versions(keep)
    if not versions:
        print("ERROR: No stable release tags found (expected vMAJOR.MINOR.PATCH).", file=sys.stderr)
        sys.exit(1)

    print(f"Found {len(versions)} version group(s) to keep (keep={keep}):")
    for i, (label, tag) in enumerate(versions):
        marker = " [current]" if i == 0 else ""
        print(f"  {label}{marker}  ← {tag}")

    if args.list:
        return

    VERSIONS_DIR.mkdir(parents=True, exist_ok=True)

    manifest = []
    for i, (version_label, tag) in enumerate(versions):
        print(f"\nFetching {version_label} from {tag}...")
        md_files = fetch_version(version_label, tag)

        nav_yml = generate_nav_yml(version_label, md_files)
        nav_path = VERSIONS_DIR / f"{version_label}.yml"
        nav_path.write_text(nav_yml)

        is_current = i == 0
        display_name = f"{version_label} (Current)" if is_current else version_label
        manifest.append(
            {
                "version_label": version_label,
                "tag": tag,
                "display_name": display_name,
                "nav_path": f"versions/{version_label}.yml",
                "slug": version_label,
            }
        )
        print(f"  → {len(md_files)} pages staged, nav → fern/versions/{version_label}.yml")

    manifest_path = VERSIONS_DIR / "manifest.json"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n")

    print(f"\nManifest written to {manifest_path.relative_to(REPO_ROOT)}")

    if args.patch_fern_docs:
        patch_fern_docs_yml(manifest)
        print(
            "\nfern/docs.yml patched — version selector is active.\n"
            "Run `fern docs dev` to preview, then restore with:\n"
            "  git checkout fern/docs.yml"
        )
    else:
        print("\nRun `fern docs dev` to preview HEAD docs.")
        print("Add --patch-fern-docs to also enable the version selector.")


if __name__ == "__main__":
    main()
