# Publishing the npm launcher

`publish-npm.yml` publishes `caliper-harness` when a stable GitHub release is published. It checks out that release's tag, checks the package and lockfile versions, and requires complete archives/checksums for macOS arm64, Windows amd64, Linux amd64 and Linux arm64. Split archives are supported. It checks the small checksum/part manifests against GitHub's asset digests and each archive/part against those digests; it does not repeat native build tests or download every multi-gigabyte archive.

The workflow runs the launcher and publication tests, packs the npm package and publishes through npm's GitHub OIDC trusted publisher. There is no npm token secret. Drafts and prereleases do not publish. Publication jobs run serially. An older release cannot move `latest` backwards.

For a manual dry run, select **Publish npm launcher → Run workflow**, choose the source branch/commit and enter the existing release tag. A draft need not have a Git tag yet: this mode validates the selected source commit against the draft's metadata. It performs the same read-only checks and packaging but never publishes or changes a release or dist-tag. A published-release event always builds from the exact release tag.

## One-time npm setup

The package must already exist before its trusted publisher can be configured. Enable two-factor authentication on the owning npm account. For the initial release, the maintainer must publish the checked package once using their own authenticated npm session, after the release and native-platform checks pass. Do not create a placeholder package. Then configure the package's trusted publisher with:

- Provider: GitHub Actions
- Organization/user: `santiagoaraoz2001-sketch`
- Repository: `caliper-harness`
- Workflow filename: `publish-npm.yml`
- Environment: none (the workflow does not use an environment)
- Permission to publish releases enabled

Use npm 11.15.0 or newer for the trust-management command. The workflow pins npm 11.15.0 on Node 24 and needs `id-token: write`. npm creates provenance for supported public-repository trusted publications automatically. See [npm trusted publishers](https://docs.npmjs.com/trusted-publishers/) and [npm trust](https://docs.npmjs.com/cli/v11/commands/npm-trust/).

The first GitHub release event can fail if this setup is not finished. After bootstrap and trust setup, rerun that event. An existing version is accepted only when its registry integrity and downloaded tarball bytes exactly match the newly packed candidate, and `latest` already points to it. A different package, or a mismatched `latest` tag, fails with an explicit diagnostic; the workflow does not overwrite an immutable npm version or silently retag it. Keep the bootstrap packer's npm version aligned with the workflow.

## Local checks

```sh
node --test scripts/check-npm-release.test.cjs
npm ci --prefix scripts/npm --ignore-scripts
npm test --prefix scripts/npm
```

The publication helper and these maintainer instructions are not included in the installed npm package. The wrapper downloads the full platform release; no model weights are included.
