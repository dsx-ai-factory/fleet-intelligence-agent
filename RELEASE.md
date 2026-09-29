# Release Process

Fleet Intelligence Agent releases are created by CI from protected version
tags. Release artifacts must not be built or published manually.

## Ownership and cadence

Project maintainers publish releases as needed. There is no fixed release
schedule. Only maintainers authorized to create protected `v*` tags may trigger
an official release.

Contributors may propose release-related changes through pull requests, but
must not create, move, or replace release tags. Published versions are
immutable.

## Versions and branches

Release tags use semantic versions with a `v` prefix:

- Stable release: `v1.5.0`
- Prerelease: `v1.5.1-rc.1`

Create a `release/<major>.<minor>` branch for each minor release series, for
example `release/1.5`. Create the branch from a reviewed commit on `main` that
has passed the required checks. Patch release fixes must be merged to `main`
first, then cherry-picked into each affected release branch.

## Prepare a release

1. Confirm `main` is current and all required CI checks pass.
2. Review the changes since the previous release and select the next semantic
   version.
3. Include migration guidance for any incompatible configuration, API, state,
   or deployment change.
4. Run the full test suite and build release packages locally without
   publishing:

   ```bash
   make docker-test
   make package-snapshot
   ```

5. Merge any required release fixes through the normal pull request process.
6. For a patch release, cherry-pick the reviewed fix into the corresponding
   release branch:

   ```bash
   git switch release/1.5
   git pull --ff-only origin release/1.5
   git cherry-pick -x <commit-sha>
   git push origin release/1.5
   ```

## Trigger a release

An authorized maintainer creates and pushes the protected version tag from the
corresponding release branch:

```bash
git switch release/1.5
git pull --ff-only origin release/1.5
git tag -a v1.5.0 -m "Release v1.5.0"
git push origin v1.5.0
```

Pushing the tag starts
[the release workflow](https://github.com/dsx-ai-factory/fleet-intelligence-agent/blob/main/.github/workflows/release.yml).
The workflow validates the version, refuses to overwrite an existing release,
and then:

1. Builds Linux AMD64 and ARM64 archives, DEB packages, and RPM packages with
   GoReleaser.
2. Signs the Linux packages and checksum manifest and verifies the signatures.
3. Publishes stable DEB packages to Artifactory.
4. Builds and publishes the agent and OTel collector container images to GHCR.
5. Packages and publishes the Helm chart to GHCR.
6. Publishes the verified GitHub release and its generated release notes.
7. Publishes the versioned documentation for stable releases.

Prerelease tags produce prerelease artifacts and do not update stable package
channels or `latest` container tags.

## Failure handling

Do not move, delete, or reuse a version tag after the workflow starts. If a
release fails, preserve the workflow logs and draft release for diagnosis, fix
the cause through a pull request, and publish a new patch or prerelease version.
