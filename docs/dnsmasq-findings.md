# dnsmasq behaviour findings

What the integration probes in `test/integration/` observed when a real
dnsmasq serves the mock config in `testdata/dnsmasq/dnsmasq.conf` and a
hosts directory (`--hostsdir`) that the tests write to. The service design
relies on these results, so re-run the probes (`make integration-docker`)
before changing how the service writes records or signals dnsmasq.

## Supported version

- Production runs Alpine Linux 3.23.8 and dnsmasq 2.91. After the owner's
  upgrade on 2026-11-17 the DNS host reports `3.23.8` in
  `/etc/alpine-release` and
  `Dnsmasq version 2.91  Copyright (c) 2000-2025 Simon Kelley`.
- Alpine 3.23 and dnsmasq 2.91 are the minimum supported targets (owner's
  decision, 2026-11-17). The service and the probes target them and later
  releases only.
- The probe image is pinned to `alpine:3.23`, and the probe harness enforces
  the dnsmasq minimum: `startDnsmasq` fails the test at once, naming
  the minimum and the version found, when the installed dnsmasq is older than
  2.91 or its version line cannot be parsed (`checkMinVersion` in
  `test/integration/harness_test.go`). Run against 2.85 every probe fails
  immediately with
  `dnsmasq 2.91 or later is required, found 2.85 ("Dnsmasq version 2.85  Copyright (c) 2000-2021 Simon Kelley")`.

## Environment

- Probe image: `alpine:3.23` (`3.23.8`), whose `dnsmasq` package is
  `2.91-r1` on both x86_64 and aarch64. The image build fails on any other
  dnsmasq version. (`alpine:3.22` also packages 2.91 and `alpine:3.21`
  packages 2.90, as of 2026-11-17.)
- The `alpine:3.23` package lists `inotify` in its compile-time options,
  which is what reads hosts-directory changes without a signal:
  `IPv6 GNU-getopt no-DBus no-UBus no-i18n no-IDN DHCP DHCPv6 no-Lua TFTP no-conntrack ipset no-nftset auth no-DNSSEC loop-detect inotify dumpfile`.
- CI's `integration-ubuntu` job runs the same probes directly on
  `ubuntu-24.04`, whose `dnsmasq-base` is `2.91-0ubuntu0.24.04.2`. Cloud
  sessions use whatever the distribution packages; the harness refuses
  anything older than 2.91.
- Each test starts its own dnsmasq with an empty hosts directory and writes a
  single file, `managed.hosts`, by writing a dot-prefixed temporary file in
  the same directory and renaming it into place. The `TestServe*` tests
  instead run the service binary beside dnsmasq and let it write its own
  `dnsmasq-manager.hosts` the same way.

## Probes on 2.91

Every probe passes on 2.91 in `make integration-docker` (`alpine:3.23`) and
in `make integration` as a non-root user in `golang:1.27` (Debian). CI's
`integration-ubuntu` job on `ubuntu-24.04` (2026-11-17) logged
`Dnsmasq version 2.91` and passed with the earlier version-aware probes,
which on 2.91 assert the same as these. No probe needs a SIGHUP to pass.

| Probe | Observed on 2.91 | Service design |
| --- | --- | --- |
| `TestMockConfigAnswersLocalNames` | `nas.home.example.test` is 10.0.0.40, any other name under the `.home.example.test` wildcard is 10.0.0.17, `example.zip` is 127.0.0.1, all answered locally. | The mock config is a usable stand-in for the live file. More specific `address=` entries beat the wildcard. |
| `TestHostsDirRecordBeatsWildcardWithoutReload` | A record added to the hosts directory overrides the `address=/.home.example.test/` wildcard: the answer changes from 10.0.0.17 to 10.0.0.80 as soon as inotify reads the file, with no SIGHUP. | The service can add names under `<private-zone>` by writing the hosts directory alone; the wildcard does not shadow them. |
| `TestHostsDirUpdateDropsOldAddress` | Changing the record from 10.0.0.80 to 10.0.0.81 replaces the old address as soon as inotify reads the file, with no SIGHUP: only 10.0.0.81 is served. One second after a SIGHUP the answer is still only 10.0.0.81. | An address change is complete once the file is written. |
| `TestHostsDirRemovalDropsRecord` | Emptying the file makes the name fall back to the wildcard (10.0.0.17) as soon as inotify reads it, with no SIGHUP. One second after a SIGHUP the answer is still 10.0.0.17. | A removal is complete once the file is written. |
| `TestServeRESTAddUpdateDelete` | The service's managed file, `dnsmasq-manager.hosts`, with its `# Generated ...` header and `#@ format`, `#@ name` lines, is served: a REST add, change and delete of `app.home.example.test` give 10.0.0.80, then only 10.0.0.81, then the wildcard 10.0.0.17, each as soon as inotify reads the file, with no SIGHUP. | The service writes the file and nothing else; no signal is needed. |
| `TestServeAAAA` | An IPv6 address line (`fd00:10::80 app.home.example.test`) in a hosts-directory file is served as an AAAA record, changes and disappears like an A line, and leaves the name's A record (10.0.0.80) as it was. | AAAA records live in the same file as A records, one address line each. |
| `TestServeWebhookChangeSet` | external-dns v0.24.0's create, update and delete change sets for an A and an AAAA record with their `a-` and `aaaa-` TXT ownership records are served the same way. The TXT records exist only as `#@ txt` lines, and a TXT query for `a-app.home.example.test` is never answered locally. | The ownership records can live in comment lines of the served file. |
| `TestServeStateSurvivesRestart` | After a restart of the service, `GET /v1/records`, the webhook's `GET /records` and the managed file are byte for byte as before, and dnsmasq still answers. | Start-up loads its own file back. |
| `TestServeHealthzReportsDnsmasqDown` | With dnsmasq killed, `/healthz` answers 503 with `"dnsmasq":"no reply from ...: connection refused"` and `"hosts_dir":"ok"`. | The health probe notices a stopped dnsmasq. |

On an update or removal the dnsmasq log shows
`inotify: flushed 1 names read from .../managed.hosts` before it rereads the
file, which is the step that drops the old records.

## The service's file: comment and AAAA lines

A manual run with `--log-queries` on 2.91 (`alpine:3.23`), against a file
holding a header comment, `#@ format 1`, `#@ name app.home.example.test api`,
the line `10.0.0.80 app.home.example.test` and a `#@ txt {...}` line,
showed:

- **`#` and `#@` lines are comments:** dnsmasq logged
  `read .../dnsmasq-manager.hosts - 1 names`, counting only the address line,
  and logged no error for the others. No query is answered from them.
- **A type held for nothing is forwarded:** a query of a type the hosts
  directory holds nothing for is passed upstream, even when the name has a
  record of another type there or falls under an `address=` wildcard.
  `app.home.example.test AAAA` (the name has only an A line),
  `app.home.example.test TXT`, `a-app.home.example.test TXT` and
  `other.home.example.test AAAA` were each logged as
  `forwarded ... to 192.0.2.1`; the mock upstreams never answer, so dig got
  no reply. `a-app.home.example.test A` was answered by the wildcard
  (`config a-app.home.example.test is 10.0.0.17`). In production the real
  upstreams answer those queries, so a TXT query for an ownership name, or
  an AAAA query for an A-only name, is resolved upstream rather than by
  dnsmasq; the integration tests treat no reply or an empty answer as "no
  local answer".

## What this means for the service

- **Precedence:** hosts-directory records beat `address=` wildcards, so a
  managed record for a name under a wildcard domain is served.
- **Additions, updates and removals** in the hosts directory take effect
  through inotify without a reload. That is probe-verified on 2.91.
- **Deleting the managed file** also drops its records at once on 2.91:
  dnsmasq logs `inotify: ... removed` and the name falls back to the
  wildcard. This was checked by hand, not by a probe. Emptying the file and
  deleting it both work; which the service does is a design choice.
- **SIGHUP** is not needed for hosts-directory changes on 2.91. It was
  verified harmless: the update and removal probes send SIGHUP after the
  change and check the answer is unchanged one second later, and a manual
  run that sent SIGHUP immediately after each write (add, update, empty)
  gave only the file's current records at once and one second later.
  Whether the service still
  sends SIGHUP after each write as a belt-and-braces step is a service-design
  decision. If it does not, it needs neither dnsmasq's PID nor permission to
  signal it.
- **Temporary files:** dnsmasq ignores dotfiles in the hosts directory. The
  logs show inotify events only for `managed.hosts`, never for the
  `.managed.hosts.*` temporary file, so writing a dot-prefixed temporary
  file in the same directory and renaming it over the managed file is safe:
  dnsmasq never reads a partial file. The service still needs this.
- **inotify** must be in the dnsmasq build; the hosts directory relies on it
  for every change.

Not verified:

- The production build's compile-time options, including `inotify`. They
  were read on the host before the upgrade (2.85) but not since; only the
  version line was reported after it. `dnsmasq --version` prints them. The
  `alpine:3.23` package lists `inotify` (above).
- Changes outside the hosts directory, such as edits to the main config;
  those were not probed, so whether they need a SIGHUP or a restart is open.
- Behaviour of releases newer than 2.91. The 2.93 CHANGELOG says inotify
  watches are now created after dnsmasq drops privileges, which affects which
  user needs read access to the hosts directory.

## History: dnsmasq before 2.88

Until 2026-11-17 production ran Alpine Linux 3.12 and dnsmasq 2.85, and the
probes branched on the
dnsmasq version. This section records what older releases do, because it is
why the probes and the service do not support them.

dnsmasq 2.87 and earlier reread a changed hosts-directory file and add its
entries, but never drop the entries the previous version of the file
contributed. Only SIGHUP, which clears the cache and rereads every hosts
file, removes them. On 2.85 that meant:

- an update answered both the old and the new address until SIGHUP;
- an emptied file's record was still served two seconds later, until SIGHUP;
- `rm managed.hosts` logged nothing and the old records stayed served until
  SIGHUP;
- so the service would have had to send SIGHUP after every write and empty
  the managed file rather than delete it.

A manual shell run with `--log-queries` ruled out a harness bug on 2.85: the
file held only the new line, both addresses were attributed to the hosts
directory, and the doubled answer was still there three seconds later. A copy
of the probes built to assert the 2.88-and-later behaviour on every version
failed both hosts-directory probes on 2.85. The current probes assert that
behaviour and, after the owner's minimum-version decision, the harness
checks the version up front so an old dnsmasq gives one clear error instead
of probe timeouts.

The change came in 2.88, whose CHANGELOG says: "Make --hostsdir (but NOT
--dhcp-hostsdir and --dhcp-optsdir) handle removal of whole files or entries
within files." Both sides of it were tested:

| dnsmasq | Source | Update (`.80` to `.81`, no SIGHUP) | Removal (file emptied, no SIGHUP) | How it was obtained |
| --- | --- | --- | --- | --- |
| 2.85 | `alpine:3.12` (`2.85-r0`), also `alpine:3.13`, `alpine:3.14` | both `.80` and `.81` | `.80` still served | probes (`make integration-docker`) and a manual shell run |
| 2.86 | `alpine:3.16` (also in `alpine:3.15`) | both `.80` and `.81` | `.80` still served | probes (static test binary) and a manual shell run |
| 2.87 | Debian `2.87-1.1` from snapshot.debian.org, in `debian:bookworm` | both `.80` and `.81` | `.80` still served | probes (static test binary) and a manual shell run |
| 2.88 | Debian `2.88-1` from snapshot.debian.org, in `debian:bookworm` | `.81` only | wildcard `.17` | probes (static test binary) and a manual shell run |
| 2.89 | Debian `2.89-1` from snapshot.debian.org, in `debian:bookworm` | `.81` only | wildcard `.17` | manual shell run |
| 2.90 | `alpine:3.21` (also in `alpine:3.17` to `alpine:3.20`) | `.81` only | wildcard `.17` | probes (static test binary) and a manual shell run |
| 2.91 | `alpine:3.23`, `alpine:3.22`; `ubuntu:24.04` `dnsmasq-base` `2.91-0ubuntu0.24.04.2`; `golang:1.27` (Debian) | `.81` only | wildcard `.17` | probes and a manual shell run |

2.88 and 2.90 behave like 2.91 in these probes; 2.89 was checked only by a
manual shell run, with the same result. The minimum is nonetheless the
version production runs, so the probes are only kept passing on 2.91 and
later.

The two hosts-directory probes were renamed as the findings changed:
`TestHostsDirUpdateDropsOldAddress` was first `TestHostsDirUpdateWithoutReload`,
then `TestHostsDirUpdateKeepsOldAddressUntilReload`, and
`TestHostsDirRemovalDropsRecord` was first `TestHostsDirRemovalNeedsReload`.
