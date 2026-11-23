# Draft-then-Publish Releases Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A release stays a draft until its files are attached, so `releases/latest` never points at a release with no files.

**Architecture:** release-please creates the release as a draft and forces the tag. The `assets` job builds with `make dist` and then calls `scripts/publish-release.sh`, which uploads the files and publishes the release as its last act. A stub-`gh` suite proves the order and that a failed upload publishes nothing.

**Tech Stack:** bash 3.2 or newer, `gh`, GNU make, release-please 17.6.0 through `googleapis/release-please-action` v5.0.0.

**Spec:** `docs/superpowers/specs/2026-11-23-draft-then-publish-releases-design.md`

**Context:** Branch `ci/9-draft-then-publish`, cut from `main`. Issue #9.

## Global Constraints

- No new action or tool in the release workflow; `gh` is on the runner.
- The script does not build. `make dist` stays a workflow step.
- `draft` and `force-tag-creation` go together in `release-please-config.json`; neither lands without the other.
- Workflow permissions do not change.
- The suite needs only bash: a clean environment (`env -i`) whose PATH holds a stub `gh`. It never reaches GitHub.
- shellcheck clean. House comments: a doc header on each script, no narrating inline comments; YAML may be commented freely.
- No private zone, LAN address or token anywhere.
- Everything in this plan lands as one commit: `ci(release): publish a release only after its assets are attached`.

## Review Focus

1. The workflow passes an empty tag (`""`), not a missing argument. The script refuses it before any `gh` call. Case `empty tag`.
2. A re-run after a partial upload. The upload carries `--clobber`, so files already attached are replaced, not an error. Asserted in case `upload then publish`. A re-run after the release was published uploads nothing. Case `already published`.
3. An upload that fails must stop the script even though more commands follow it. Case `upload fails` asserts the call log has no `edit` line.
4. A refusal says why on stderr, so a failed job's log explains itself. The three refusal cases assert stderr is not empty.
5. A config with `"draft": false`, or with one key removed. The config case matches `"draft": true` and `"force-tag-creation": true` each on their own, so either change fails it.

---

### Task 1: publish a release only after its assets are attached

**Files:**
- Create: `scripts/publish-release.sh` (mode 755), `test/release/publish_test.sh`
- Modify: `release-please-config.json`, `Makefile`, `.github/workflows/release.yml`, `.github/workflows/ci.yml`, `README.md`, `CLAUDE.md`

**Interfaces:**
- `scripts/publish-release.sh <tag> <dir>`: exit 0 when the files in `<dir>` are attached to the release for `<tag>` and it is published. Non-zero, with a message on stderr and no `gh` call, when `<tag>` is empty or missing, `<dir>` is not a directory, or `<dir>` lacks any of `dnsmasq-manager-linux-amd64`, `dnsmasq-manager-linux-amd64.sha256` and `install.sh`. Otherwise the exit status of the failing `gh` call. `gh` calls, exactly and in this order:
  - `gh release view "$tag" --json isDraft --jq .isDraft`; anything but `true` ends the script with exit 0
  - `gh release upload "$tag" <the three files, each as "$dir/<name>"> --clobber`
  - `gh release edit "$tag" --draft=false`
- `make test-release`: runs `bash test/release/publish_test.sh`.
- Stub `gh` in the suite: appends its arguments as one line to `$GH_LOG`, and exits 7 when its second argument equals `$GH_FAIL` (`view`, `upload` or `edit`), prints `$GH_DRAFT` (default `true`) for `release view`, and exits 0 otherwise.

- [ ] **Step 1: Write the failing suite**

`test/release/publish_test.sh`, in the shape of `test/hook/hook_test.sh`: reuse its `new_case` / `stub` / `problem` helpers' form, a per-case directory with its own `bin/` on PATH, `env -i` for every run of the script, a count of problems and a non-zero exit when there are any. The suite finds the repo root from its own path. Each case's `dist` directory holds `install.sh`, `dnsmasq-manager-linux-amd64` and `dnsmasq-manager-linux-amd64.sha256` unless the case says otherwise; the tag is `v9.9.9`.

| Case | Setup | Asserts |
|---|---|---|
| `upload then publish` | none | exit 0; log has three lines; line 1 is `release view v9.9.9 --json isDraft --jq .isDraft`; line 2 starts `release upload v9.9.9 `, names all three files, not the extra file in `dist`, and ends `--clobber`; line 3 is `release edit v9.9.9 --draft=false` |
| `upload fails` | `GH_FAIL=upload` | exit 7; an upload line; no log line starts `release edit` |
| `publish fails` | `GH_FAIL=edit` | exit 7; the same three lines as the first case |
| `already published` | `GH_DRAFT=false` | exit 0; the view line; no upload or edit line |
| `draft question fails` | `GH_FAIL=view` | exit 7; no upload or edit line |
| `no tag` | called with no arguments | exit 2; log empty or absent; stderr names the reason |
| `empty tag` | tag `""` | the same |
| `directory missing` | `<dir>` does not exist | the same |
| `<file> missing`, once per release file | `dist` without that file | the same |
| `config drafts and forces the tag` | reads `release-please-config.json` with `grep -E` | one line matches `"draft"[[:space:]]*:[[:space:]]*true`; one matches `"force-tag-creation"[[:space:]]*:[[:space:]]*true` |

- [ ] **Step 2: Add `make test-release`**

In `Makefile`, beside `test-hook`: a `test-release` target running `bash test/release/publish_test.sh`, with the same style of comment, and added to `.PHONY`.

- [ ] **Step 3: Run it and see it fail**

Run: `make test-release`
Expected: non-zero exit, every case reported as a problem (the script does not exist; the config has neither key).

- [ ] **Step 4: Write `scripts/publish-release.sh`**

`#!/usr/bin/env bash`, a doc header saying what it is for, how it is called, why it refuses a directory without all three release files (an empty or half-built `dist/` must not become the latest release) and why a re-run is safe (`--clobber` and `--draft=false` both repeat cleanly). `set -eu`. The three refusals first, each with its own message on stderr and exit 2. Then the two `gh` calls from the Interfaces block.

- [ ] **Step 5: Set the config keys**

In `release-please-config.json`, add to the `.` package:

```json
"draft": true,
"force-tag-creation": true
```

- [ ] **Step 6: Run it and see it pass**

Run: `make test-release`
Expected: exit 0, no problems.

- [ ] **Step 7: Call the script from the release workflow**

In `.github/workflows/release.yml`, the last step of `assets` is named `Build, upload and publish the release` and runs:

```sh
make dist VERSION="$TAG"
bash scripts/publish-release.sh "$TAG" dist
```

`env` is unchanged. Remove the "Known gap" comment. The header comment, the `release-please` job comment and the `assets` job comment say: the release is created as a draft with its tag forced, and stays a draft until the `assets` job has attached the files and publishes it; if that job fails the release stays a draft, `latest` stays on the previous release, and re-running the job finishes it. The checkout comment notes that the tag exists because `force-tag-creation` is set. The `permissions` comment on `assets` says upload and publish.

- [ ] **Step 8: Run the suite and shellcheck in CI**

In `.github/workflows/ci.yml`, job `lint-and-unit`: `make test-release` in the step after `make test-hook` (or the same step, whichever matches how `test-hook` is run), and `scripts/publish-release.sh` and `test/release/publish_test.sh` added to shellcheck in the way the installer and the hook tests are checked there. Update the job comment to name the release script.

- [ ] **Step 9: Docs**

- `README.md`, Releasing section: replace the "Known gap" bullet with the behaviour (draft until the `assets` job attaches the files and publishes; a failed job leaves a draft and `latest` on the previous release). Keep the "re-run the `assets` job" recovery. Add `make test-release` to the commands table.
- `CLAUDE.md`, Commands: add `make test-release`: tests `scripts/publish-release.sh` against a stub `gh`; needs only bash.

- [ ] **Step 10: Verify**

Run: `make test-release && make test-hook && make test-install`
Expected: all exit 0.

Run: `shellcheck scripts/publish-release.sh test/release/publish_test.sh`
Expected: no output.

Run: `actionlint .github/workflows/release.yml .github/workflows/ci.yml` when `actionlint` is installed.
Expected: no output. When it is not installed, the CI run on the pull request is the check for `ci.yml`.

`release.yml` runs only on `main`. Its proof is the next real release, as the spec's "Checking it on a real release" section sets out.

- [ ] **Step 11: Commit**

```bash
git add scripts/publish-release.sh test/release/publish_test.sh release-please-config.json Makefile .github/workflows/release.yml .github/workflows/ci.yml README.md CLAUDE.md docs/superpowers/plans/2026-11-23-draft-then-publish-releases.md
git commit -m "ci(release): publish a release only after its assets are attached"
```
