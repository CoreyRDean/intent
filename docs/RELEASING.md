# Releasing

This document describes how stable and nightly releases of `intent` are produced. It is normative — if any of this drifts from reality, fix the drift, don't update the doc to match.

## Channels

| Channel | Tag shape | Triggered by | Pre-release? |
|---|---|---|---|
| **stable** | `vMAJOR.MINOR.PATCH` (e.g. `v0.3.1`) | Manual: `git tag` + `git push` | no |
| **nightly** | `vMAJOR.MINOR.PATCH-<unix-timestamp>` (e.g. `v0.3.2-1745190123`) | Cron: `.github/workflows/nightly.yml` runs at 07:00 UTC daily | yes |

Both channels build the same binary from the same code path. They differ only in version metadata baked in at build time and in whether the GitHub Release is marked pre-release. See `docs/DECISIONS.md` D-005 for the rationale.

## What happens when a tag is pushed

1. `.github/workflows/release.yml` triggers on any tag matching `v*`.
2. It cross-builds for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.
3. Each build is `tar.gz`-bundled with `INTENT.md`, `README.md`, `LICENSE`.
4. SHA256SUMS is generated from all artifacts.
5. A GitHub Release is created at the tag with all artifacts attached.
6. The release is marked **pre-release** if the tag has a pre-release suffix (anything after `-`), and **stable** otherwise.

## How nightly works

`.github/workflows/nightly.yml` runs every day at 07:00 UTC:

1. Find the latest stable tag (a `v*` tag whose name does **not** match `v\d+\.\d+\.\d+-\d+`). If none exists, treat the base version as `0.0.0`.
2. Patch-bump the base version. (e.g. base `v0.3.1` → next `0.3.2`.)
3. Build the new tag as `v0.3.2-$(date +%s)`.
4. If `HEAD` already has a nightly tag, skip — we don't ship two nightly builds for the same commit.
5. Create the annotated tag and `git push origin "$tag"` — which triggers the `release` workflow.

Result: every successful day on `main` produces exactly one nightly pre-release; every push to a `v*` tag produces exactly one release of the matching channel.

## Cutting a stable release

```bash
# bump the patch (or minor / major as appropriate)
git switch main
git pull --ff-only

git tag -a v0.4.0 -m "v0.4.0"
git push origin v0.4.0
```

That's it. The release workflow does the rest.

## Backing out a release

If something is wrong with a published release (binary, not just the notes):

1. Edit the release on GitHub and mark it as a draft (this hides it from the API the install script and update checker hit).
2. Delete the underlying tag from the repo: `git push origin :refs/tags/vX.Y.Z`
3. Cut a new release one patch number higher with the fix.

We don't yank-and-rewrite tags. The install script verifies SHA256SUMS, so a republished tag with a different binary would invalidate any cached installs.

## Versioning policy

- We follow [SemVer 2.0.0](https://semver.org/) for stable releases.
- Pre-1.0: minor bumps may include breaking changes; patch bumps may not.
- Post-1.0: standard SemVer rules.
- The `INTENT.md` constitution and the public CLI surface in `docs/SPEC.md` are the surface area whose breakage requires a major bump.

## Homebrew tap (planned)

The Homebrew tap will live in a separate repo, `CoreyRDean/homebrew-intent`, with `Formula/intent.rb` regenerated on each stable release by an additional job in `release.yml`. Until that is set up, install via `install.sh` or by downloading the binary directly from the Releases page.

## Self-update (planned)

`i update now` will, once Phase 5 self-update lands:

1. Resolve the latest version on the configured channel.
2. Download the appropriate `tar.gz` and `SHA256SUMS` to the cache directory.
3. Verify the checksum.
4. Atomically rename the new binary into place over the running one (POSIX-safe; the running process keeps its open file handle).
5. Print the new version and exit successfully.

In v1, `i update now` reports the available version and tells the user to use Homebrew or the install script.
