# Draft-then-publish releases

Issue: #9. Part of the roadmap epic #3.

## Intent

release-please publishes the GitHub release as soon as the release pull
request merges. The `assets` job then builds the release files and uploads
them. Until that job finishes, `releases/latest` points at a release with no
files, so the install one-liner in the README fails at download. On `v0.1.0`
the release was published at 19:58:59 and its files arrived at 19:59:32: a
33-second window.

This change keeps a new release hidden until its files are attached. It is
created as a draft, and the job that uploads the files publishes it as its
last act.

## Goals

1. `releases/latest/download/install.sh` never returns 404 during a release.
2. If building or uploading the files fails, the release stays a draft and
   `latest` stays on the previous release.
3. A failed release can be finished by re-running the failed job, with no
   hand edits.
4. release-please still finds the previous release on its next run, so the
   next changelog covers only the commits since it.

## Non-goals

- arm64 files (#7), pinning the other actions by SHA (#8).
- Changing what `make dist` produces or how `install.sh` downloads.
- Guarding an install pinned to the exact version being released. Its tag
  exists while the release is still a draft, so
  `DNSMASQ_MANAGER_VERSION=<that version>` fails at download for the length
  of the `assets` job. Nobody can know the version to pin before it is
  published, so this is accepted.

## Anti-goals

- A second workflow started by the release. A release created with
  `GITHUB_TOKEN` starts no other workflow, so that needs a personal token or
  a `workflow_run` trigger. The `assets` job is already the second stage.
- A release that is published without its files because a check was skipped
  to keep the workflow short.
- A new action or tool in the release workflow. `gh` is on the runner.

## Verified facts this design rests on

Checked on 2026-11-23.

- `release-please-config.json` sets none of `draft`, `prerelease` or
  `force-tag-creation`. The manifest is at `0.1.0`.
- The workflow pins `googleapis/release-please-action` v5.0.0, whose lock
  file resolves `release-please` to 17.6.0. The action has no `draft` input;
  the setting is read from the config file (`$.draft` or
  `$.packages[path].draft`).
- release-please 17.6.0 has `draft` and `force-tag-creation` in its config
  schema. Its docs say GitHub does not create the tag for a draft release
  until it is published, that release-please then fails to find the previous
  release, and that `force-tag-creation: true` creates the tag immediately.
  The option was added in 17.2.0.
- The `assets` job checks out `tag_name` before building, so the tag must
  exist while the release is still a draft.
- The action's README documents one way to attach files: a `gh release
  upload` step gated on `release_created`. release-please has no hook that
  runs between creating a release and publishing it.
- Immutable releases are off for the repository.
- The `release` concurrency group runs one release workflow at a time and
  never cancels one, so a later push to `main` waits for a release in
  progress.

Not verified, and only a real release can settle them:

- `gh release view <tag>`, `gh release upload <tag>` and
  `gh release edit <tag>` find a draft release
  by its tag with the workflow's `contents: write` token.
- Publishing a draft whose tag already exists keeps that tag and marks the
  release as latest.
- With a draft left over from a failed run, the next release-please run still
  takes the forced tag as the previous release.

## Approaches

**A. Draft, forced tag, publish from a tested script (chosen).**
release-please creates the release as a draft and creates the tag itself. A
small script uploads the files and then publishes; the `assets` job calls it.
A stub test proves the order and that a failed upload publishes nothing.
Trade-off: one more file and one more make target than the inline form.

**B. The same, inline in the workflow.** One extra `gh release edit` line in
`release.yml`. The smallest diff, but nothing checks it before a real
release, and the repo's workflow asks for a failing test first.

**C. Prerelease instead of draft.** GitHub never treats a prerelease as
latest, and the tag exists at once, so no forced tag is needed. Dropped:
release-please 17.6.0 applies `prerelease` only while the major version is 0
or the version has a prerelease suffix, so the gap would return silently at
`v1.0.0`. The release would also be public while empty.

**D. Create the release with its files in one step.** Set
`skip-github-release` and have the job run `gh release create` with the
files. Dropped: the job would have to rebuild the release notes that
release-please writes today, and release-please's docs say it still needs
the tags made for it.

Gavin chose the draft route over the prerelease route. Putting the steps in
a script (A rather than B) is the builder's call, for the test.

## Design

### `release-please-config.json`

Two keys added to the `.` package:

```json
"draft": true,
"force-tag-creation": true
```

They go together. `draft` without the forced tag breaks the `assets`
checkout and the next changelog.

### `scripts/publish-release.sh`

A bash script, called as `scripts/publish-release.sh <tag> <dir>`. In order:

1. Refuses, before any `gh` call, when the tag is empty, when `<dir>` is not
   a directory, or when `<dir>` lacks any of the three release files
   (`dnsmasq-manager-linux-amd64`, `dnsmasq-manager-linux-amd64.sha256`,
   `install.sh`). An empty or half-built `dist/` must not become the latest
   release. Review found that checking `install.sh` alone leaned on the
   order of the `make dist` recipe, so all three are checked by name.
2. `gh release view "$tag" --json isDraft --jq .isDraft`. When the release
   is no longer a draft it uploads nothing and exits 0.
3. `gh release upload "$tag" <the three files> --clobber`. Only the files
   checked in step 1 are uploaded, whatever else `<dir>` holds.
4. `gh release edit "$tag" --draft=false`.

It stops at the first failure, so step 4 runs only after step 3 succeeded.
A refusal exits 2; a failed `gh` call exits with that call's status.
`--clobber` makes the upload safe to repeat while the release is a draft,
which is what makes a re-run of the failed job a complete recovery. Step 2
exists because the upload is not safe to repeat on a published release:
replacing a file removes it first, so `latest` would be without it for a
moment. Review raised this for a job re-run after it succeeded, or after a
publish that was applied but reported as failed.

The script does not build. `make dist` stays a workflow step, so the
Makefile remains the only build recipe.

### `.github/workflows/release.yml`

The last step of the `assets` job becomes:

```sh
make dist VERSION="$TAG"
bash scripts/publish-release.sh "$TAG" dist
```

The checkout by tag is unchanged; it now relies on the forced tag. The
"Known gap" comment is removed, and the header and job comments say that the
release is a draft until this job publishes it. Permissions do not change:
`contents: write` already covers uploading to and editing a release.

### Tests

`test/release/publish_test.sh`, run by a new `make test-release` target and
by the CI job that runs `make test-hook`. Same style as the hook tests: a
clean environment whose PATH holds a stub `gh` that logs its arguments and
fails on request. Needs only bash.

| Case | Expect |
|---|---|
| Upload and publish succeed | exit 0; `gh` called three times, all with the tag: the draft question, the upload of exactly the three files, then edit with `--draft=false` |
| Upload fails | exits with `gh`'s status; no `gh release edit` call |
| Publish fails | exits with `gh`'s status |
| Release already published | exit 0; no upload and no edit |
| Draft question fails | exits with `gh`'s status; no upload and no edit |
| No tag given | exit 2; no `gh` call |
| Directory missing | exit 2; no `gh` call |
| Any one of the three release files missing from the directory | exit 2; no `gh` call |
| `release-please-config.json` | `draft` and `force-tag-creation` are both `true` |

The last case fails if either key is removed on its own.

### Docs

The Releases section of `README.md` and the command list in `CLAUDE.md` gain
the draft step, the new make target and the recovery: re-run the failed
`assets` job.

### Checking it on a real release

The stub test cannot show what GitHub does. The first release cut after this
lands is the check; today that is `0.2.0`, the open release pull request.
When Gavin merges it:

1. While the `assets` job runs: `v0.2.0` exists as a tag, the release is a
   draft, and `releases/latest/download/install.sh` still serves `v0.1.0`.
2. After it: the release is published with three files, and `latest` is
   `v0.2.0`.
3. On the next push to `main`: the new release pull request lists only the
   commits since `v0.2.0`.

This covers the three unverified facts above, apart from the leftover-draft
case, which needs a failed release and is left until one happens.

Issue #9 carries the task "Confirm a draft release still gets its tag, and
that the next release-please run finds the previous release". Merging this
change closes the issue before that check can run, so the result is recorded
as a comment on #9 after `0.2.0`, and the issue is reopened if a step fails.

## Left to the builder

- The exact wording of the script's error messages and of the comments in
  `release.yml`.
- Whether the config case reads the JSON with `grep` or with `jq` when it is
  present; the suite must still pass with bash alone.
