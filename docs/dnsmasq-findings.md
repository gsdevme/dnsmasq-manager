# dnsmasq behaviour findings

What the integration probes in `test/integration/` observed when a real
dnsmasq serves the mock config in `testdata/dnsmasq/dnsmasq.conf` and a
hosts directory (`--hostsdir`) that the tests write to. The service design
relies on these results, so re-run the probes (`make integration-docker`)
before changing how the service writes records or signals dnsmasq.

## Environment

- Production host: Alpine 3.12, dnsmasq 2.85.
- Probe image: `alpine:3.12`, whose `dnsmasq` package is `2.85-r0`, so no
  newer Alpine release was needed. The image build fails on any other version.
- dnsmasq is built with `inotify`, which is what reads hosts-directory
  changes without a signal.
- Each test starts its own dnsmasq with an empty hosts directory and writes a
  single file, `managed.hosts`, by writing a dot-prefixed temporary file in
  the same directory and renaming it into place.
- The probes also run against newer dnsmasq: CI's `integration-ubuntu` job
  and cloud sessions use whatever the distribution packages. dnsmasq changed
  how it handles hosts-directory updates and removals in 2.88 (see
  [Version difference](#version-difference-288)), so the two probes that
  depend on it pick their assertions from the dnsmasq version
  (`hostsDirFlushes` in `test/integration/harness_test.go`).

## Probes

| Probe | Expected | Observed on 2.85 | Observed on 2.88 and later | Service design | Ubuntu CI version |
| --- | --- | --- | --- | --- | --- |
| `TestMockConfigAnswersLocalNames` | Exact `address=` names, `.home.example.test` wildcards and `.zip` answer locally. | As expected: `nas.home.example.test` is 10.0.0.40, any other name under the wildcard is 10.0.0.17, `example.zip` is 127.0.0.1. | Same as 2.85. | The mock config is a usable stand-in for the live file. More specific `address=` entries beat the wildcard. | 2.91 (`2.91-0ubuntu0.24.04.2`): same as 2.85. Observed in an ubuntu:24.04 container; to be confirmed from the CI log. |
| `TestHostsDirRecordBeatsWildcardWithoutReload` | A hosts-directory record overrides the `address=/.home.example.test/` wildcard, with no SIGHUP. | As expected: the answer changes from 10.0.0.17 to 10.0.0.80 as soon as inotify reads the file. | Same as 2.85. | The service can add names under `<private-zone>` by writing the hosts directory alone; the wildcard does not shadow them. | 2.91: same as 2.85. Observed in an ubuntu:24.04 container; to be confirmed from the CI log. |
| `TestHostsDirUpdateDropsOldAddress` (originally `TestHostsDirUpdateWithoutReload`, then `TestHostsDirUpdateKeepsOldAddressUntilReload`) | Changing a record's address replaces the old answer without a SIGHUP. | **Differs.** The new address is added but the old one is kept: the name answers both 10.0.0.80 and 10.0.0.81 until SIGHUP, after which only 10.0.0.81 is served. | As expected: only 10.0.0.81 is served as soon as inotify reads the file. A SIGHUP afterwards still serves only 10.0.0.81. | On 2.85 an address change is not complete until the service sends SIGHUP; without it clients get both addresses. | 2.91: as on 2.88 and later. Observed in an ubuntu:24.04 container; to be confirmed from the CI log. |
| `TestHostsDirRemovalDropsRecord` (originally `TestHostsDirRemovalNeedsReload`) | Emptying the file leaves the removed record served until SIGHUP. | As expected: 10.0.0.80 is still served two seconds after the file is emptied; after SIGHUP the name falls back to the wildcard (10.0.0.17). | **Differs from the expectation.** The name falls back to the wildcard (10.0.0.17) as soon as inotify reads the emptied file. A SIGHUP afterwards still answers 10.0.0.17. | On 2.85 removing a record needs SIGHUP. | 2.91: as on 2.88 and later. Observed in an ubuntu:24.04 container; to be confirmed from the CI log. |

## What this means for the service

- **Precedence:** hosts-directory records beat `address=` wildcards, so a
  managed record for a name under a wildcard domain is served.
- **Additions** take effect without a signal, on every version tested.
- **Updates and removals** on 2.85 (production) do not: dnsmasq 2.85
  rereads a changed hosts file and adds its entries, but never drops the
  entries the previous version of the file contributed. Only SIGHUP (which
  clears the cache and rereads every hosts file) removes them. From 2.88
  dnsmasq drops them itself.
- **The rule is: send SIGHUP after every write of the managed file.** It is
  required on 2.85 and correct and harmless on 2.88 and later. The probes
  send SIGHUP after each update and removal and assert the answer is still
  right on every version. A manual run also sent SIGHUP immediately after
  each write (add, update, empty) and the next query, at once and one second
  later, gave only the file's current records on 2.85, 2.86, 2.87, 2.88,
  2.89, 2.90 and 2.91.
- **Deleting the file** is not acted on by 2.85: a manual check showed that
  `rm managed.hosts` logs nothing and the old records keep being served
  until SIGHUP. From 2.88 dnsmasq logs `inotify: ... removed` and drops the
  file's records at once. The service should empty the file rather than
  delete it, and signal either way.
- **Temporary files:** dnsmasq ignores dotfiles in the hosts directory. The
  logs show inotify events only for `managed.hosts`, never for the
  `.managed.hosts.*` temporary file, so writing a dot-prefixed temporary
  file in the same directory and renaming it over the managed file is safe:
  dnsmasq never reads a partial file.

## Version difference (2.88)

dnsmasq 2.88 and later drop the records a hosts-directory file contributed
when inotify sees that file change or disappear; 2.87 and earlier keep them
until SIGHUP. On 2.91 the log shows the extra step as
`inotify: flushed 1 names read from .../managed.hosts` before the reread.

| dnsmasq | Source | Update (`.80` to `.81`, no SIGHUP) | Removal (file emptied, no SIGHUP) | How it was obtained |
| --- | --- | --- | --- | --- |
| 2.85 | `alpine:3.12` (`2.85-r0`), also `alpine:3.13`, `alpine:3.14` | both `.80` and `.81` | `.80` still served | probes (`make integration-docker`) and a manual shell run |
| 2.86 | `alpine:3.16` (also in `alpine:3.15`) | both `.80` and `.81` | `.80` still served | probes (static test binary) and a manual shell run |
| 2.87 | Debian `2.87-1.1` from snapshot.debian.org, in `debian:bookworm` | both `.80` and `.81` | `.80` still served | probes (static test binary) and a manual shell run |
| 2.88 | Debian `2.88-1` from snapshot.debian.org, in `debian:bookworm` | `.81` only | wildcard `.17` | probes (static test binary) and a manual shell run |
| 2.89 | Debian `2.89-1` from snapshot.debian.org, in `debian:bookworm` | `.81` only | wildcard `.17` | manual shell run |
| 2.90 | `alpine:3.21` (also in `alpine:3.17` to `alpine:3.20`) | `.81` only | wildcard `.17` | probes (static test binary) and a manual shell run |
| 2.91 | `alpine:3.22`; `ubuntu:24.04` `dnsmasq-base` `2.91-0ubuntu0.24.04.2`; `golang:1.27` (Debian) | `.81` only | wildcard `.17` | probes (`make integration` as a non-root user in `golang:1.27`, static test binary as a non-root user in `ubuntu:24.04`) and a manual shell run in `alpine:3.22` |

The boundary matches the dnsmasq 2.88 CHANGELOG entry: "Make --hostsdir
(but NOT --dhcp-hostsdir and --dhcp-optsdir) handle removal of whole files
or entries within files." Both sides of it, 2.87 and 2.88, were tested, so
the 2.88 threshold in `hostsDirFlushes` is not an assumption. Versions older
than 2.85 are assumed to behave like 2.85, and versions newer than 2.91 are
assumed to behave like 2.91.

## Option: upgrading dnsmasq on the DNS host

The owner raised on 2026-11-17 that production dnsmasq could likely be
upgraded to remove the version difference. This is an open decision for the
owner and the service design, not a recommendation to act now; the live
host has not been touched.

Verified:

- 2.88 is the first release that drops a changed or removed hosts file's old
  records without SIGHUP (table above).
- Of the Alpine images checked, `alpine:3.15` and `alpine:3.16` package
  2.86, `alpine:3.17` to `alpine:3.21` package 2.90 and `alpine:3.22`
  packages 2.91 (as of 2026-11-17). So Alpine 3.17 is the oldest Alpine
  release whose current package has the newer behaviour.
- On 2.88 and later the hosts-directory probes pass without a SIGHUP for
  updates and removals, and deleting the managed file drops its records.

What the service could drop if production ran 2.88 or later:

- The SIGHUP after every write, and with it the need to know dnsmasq's PID or
  have permission to signal it.
- The rule to empty the managed file rather than delete it.

What would still be needed:

- inotify support in the dnsmasq build, which the hosts directory relies on
  for every change (the Alpine 2.90 and 2.91 builds list `inotify`).
- The dot-prefixed temporary file and rename, so dnsmasq never reads a
  partial file.
- Whatever the service does for changes outside the hosts directory, such as
  edits to the main config; those were not probed.

Not verified:

- Whether a newer dnsmasq package can be installed on Alpine 3.12 without
  upgrading the whole host, and what an Alpine upgrade from 3.12 would
  change elsewhere on the host.
- Whether Alpine 3.17's package was 2.88 or later when 3.17 was first
  released; only today's package (2.90) was checked.
- Behaviour of releases newer than 2.91. The 2.93 CHANGELOG says inotify
  watches are now created after dnsmasq drops privileges, which affects which
  user needs read access to the hosts directory.

## How the changed probes were checked

### On 2.85

The update probe failed with `last answer [10.0.0.80 10.0.0.81]`. To rule out
a harness bug, the same steps were repeated by hand in the probe image with a
shell script (`mv` of a temporary file, then an in-place overwrite) and
`--log-queries`:

- the file held only the new line (`10.0.0.81 app.home.example.test`);
- dnsmasq logged `read .../managed.hosts - 1 addresses` for each write, then
  answered the query with both addresses, each attributed to the hosts
  directory rather than the cache of an upstream answer;
- the doubled answer was still there three seconds later, so it is not a
  timing effect;
- after SIGHUP dnsmasq logged `cleared cache`, reread the file and answered
  only the new address.

### On 2.88 and later

The update and removal probes failed on 2.91 (`last answer [10.0.0.81]`, and
`before reload = [10.0.0.17]`). The dnsmasq log in the failure output shows
`inotify: flushed 1 names read from .../managed.hosts` before each reread,
a line 2.85 never prints. The same shell script as above, run outside the Go
harness in each image in the table, gave the results in the table. On 2.91
its query log attributes `.81` to `managed.hosts` and, after the file is
emptied or deleted, `.17` to the config wildcard. A copy of the probes built to take the 2.88 branch on every
version failed both probes on 2.85, so each branch asserts something the
other version does not do.
