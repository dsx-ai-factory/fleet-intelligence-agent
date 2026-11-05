# Release Process

Fleet Intelligence Agent releases are created by CI from protected version
tags. Release artifacts must be built and published through the approved
release process.

## Ownership and cadence

Project maintainers publish releases as needed. There is no fixed release
schedule. Only authorized NVIDIA employees may create official releases at
this time. They must have permission to create protected `v*` tags and the
required release-system access. Maintainer status or GitHub write access alone
does not grant publishing access.

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

## Stage and approve images and the chart

Before creating the protected version tag, run **Stage NGC release** on the
reviewed `release/<major>.<minor>` branch with the final version, such as
`v1.6.0` or `v1.6.0-rc.1`. The workflow runs the agent tests, builds the agent
and gateway images for Linux AMD64 and ARM64, and uploads them and the
final-version chart to private NGC staging. It attaches SBOMs and provenance
to the images and retains signed provenance, image digests, the chart
checksum, and the source commit in review artifacts for 90 days. Staging does
not update `latest`, create a tag, publish documentation, or publish a GitHub
release.

Complete the [review and promotion](#review-and-promotion) steps for these
exact artifacts. Keep the release branch at the approved source commit. The
public NGC artifacts must be available and their provenance must identify that
commit before the tagged release can publish Linux packages and the GitHub
release. Preserve NGC image attestation referrers during promotion so the
tagged release can verify them.

## Trigger a release

After NGC promotion and verification, an authorized maintainer creates and
pushes the protected version tag from the approved release branch:

```bash
git switch release/1.5
git pull --ff-only origin release/1.5
```

Before creating the tag, push the release branch and wait for all required CI
checks to pass for the exact commit at the branch tip. Verify that the passing
workflow run's commit SHA matches `git rev-parse HEAD`; checks on `main` or an
earlier release-branch commit do not qualify. Fetch the release branch again
immediately before tagging and stop if its remote tip no longer matches `HEAD`.

```bash
git fetch origin release/1.5
test "$(git rev-parse HEAD)" = "$(git rev-parse origin/release/1.5)" || {
  echo "release branch advanced; update it and repeat CI verification"
  exit 1
}
git tag -a v1.5.0 -m "Release v1.5.0"
git push --atomic origin HEAD:release/1.5 v1.5.0
```

The atomic push prevents publishing the tag if the remote release branch
advances after the comparison.

Pushing the tag starts
[the release workflow](https://github.com/dsx-ai-factory/fleet-intelligence-agent/blob/main/.github/workflows/release.yml).
The workflow validates the version, refuses to overwrite an existing release,
and then:

1. Verifies the public NGC images and chart have signed provenance from the
   staging workflow for the exact tagged source commit. Missing or mismatched
   NGC artifacts stop the release before any package publication.
2. Builds Linux AMD64 and ARM64 archives, DEB packages, and RPM packages with
   GoReleaser.
3. Signs the Linux packages and checksum manifest and verifies the signatures.
4. Publishes stable DEB packages to Artifactory.
5. Publishes the verified GitHub release with generated notes, public NGC
   links, and the registry migration instructions.
6. Publishes the versioned documentation for stable releases.

Prerelease tags produce prerelease artifacts and do not update stable package
channels. Container image versions are published through NGC; this workflow
does not update GHCR tags.

## Release artifacts and NGC publication

Use the `fleet-intelligence` team in the NGC `dsx-ai-factory` staging
organization. Public images and the chart are distributed through the
`nvidia/fleet-intelligence` namespace:

| Artifact | Public name or location |
| --- | --- |
| Agent image | `nvcr.io/nvidia/fleet-intelligence/agent` |
| Shared telemetry gateway image | `nvcr.io/nvidia/fleet-intelligence/otel-gateway` |
| Helm chart name | `fleet-intelligence-agent` |
| Helm repository | `https://helm.ngc.nvidia.com/nvidia/fleet-intelligence` |

New releases publish the agent and gateway images and the chart only to NGC.
GHCR receives no new versions or `latest` updates. Existing installations
should follow [Upgrade from GHCR](docs/install-helm.md#upgrade-from-ghcr) to
select the NGC chart and replace saved image repository and version overrides.
This repository owns both image builds; the gateway image is also consumed by
the OOB collector deployment.

### Publishing access

The authorized release maintainer needs membership in the NGC `dsx-ai-factory`
staging organization and publishing access to its `fleet-intelligence` team.
Configure the GitHub environment `ngc-publish` with the environment secret
`NGC_API_KEY`, environment variable `NGC_ORG` containing the private staging
organization identifier, selected release branch rules such as `release/*`,
and any required reviewers. The staging team is fixed to `fleet-intelligence`;
CI rejects `nvidia` as the staging organization. API key authentication is used until the required OIDC
publishing support is available.

The tagged release runner requires Python 3, Helm, and a GitHub
CLI that supports attestation verification with `--source-digest` and
`--signer-workflow`. CI installs the pinned Crane CLI to resolve the public
image digests before verification. The tagged release only reads NGC
artifacts; public image and chart publication happens through the approved
NGC promotion process.

Keep keys in GitHub secrets and internal operational records. Detailed access,
security review, and promotion instructions remain in NVIDIA's internal
records.

### Review and promotion

1. Build the final-version images and chart from the reviewed release commit
   through CI. Upload images to
   `nvcr.io/<staging-org-id>/fleet-intelligence/agent` and
   `nvcr.io/<staging-org-id>/fleet-intelligence/otel-gateway`, and the chart to
   `<staging-org-id>/fleet-intelligence/fleet-intelligence-agent`.
2. Record the source commit, image digests, chart checksum, SBOMs, and signed
   provenance, and test those exact artifacts. The chart must use its final
   public image references before review; staging tests can override image
   repositories at installation time without changing the package.
3. Complete NVIDIA's required security and compliance checklists and
   open-source review requirements. Record approval for the image digests and
   chart checksum. A successful build alone does not authorize public
   publication.
4. Submit the internal publishing request to promote the approved artifacts.
   Preserve image digests, chart bytes, SBOMs, and provenance; do not rebuild
   or repackage during promotion.
5. Verify public access to both images and the chart, compare the public
   digests and checksum with the approved records, and verify provenance.
   Include the public NGC links in the release notes. See the
   [Helm installation guide](docs/install-helm.md) for customer commands.

Failed or pending checks block promotion. Changes to an artifact require
renewed testing and approval. Linux package signing and Artifactory
publication follow the release workflow described above.

## Failure handling

Do not move, delete, or reuse a version tag after the workflow starts. If a
release fails, preserve the workflow logs and draft release for diagnosis, fix
the cause through a pull request, and publish a new patch or prerelease version.
