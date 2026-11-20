# Requirements: dnsmasq-manager service

This is the input to the service design. The original requirements, handed
over from the owner's homelab session, are at the end of this file unchanged.
The two sections before them take precedence where they differ: corrections
found by checking the real DNS host, dnsmasq 2.91 and external-dns v0.24.0,
and additions from the owner. The zone is written as `<private-zone>`
throughout; the real zone is supplied as config.

## Corrections from verification

- The DNS host runs Alpine Linux 3.23.8 with dnsmasq 2.91 (owner-reported on
  2026-11-17, after the upgrade). It was x86_64 when inspected before the
  upgrade. Alpine uses OpenRC, not systemd: an OpenRC init script replaces
  the systemd unit, an `rc-service` reload replaces `systemctl reload`, and
  there is no journald, so "stdout/journald" logging means stdout.
- dnsmasq reports `Dnsmasq version 2.91  Copyright (c) 2000-2025 Simon Kelley`.
  There is a single instance on one server. No replication is needed. The
  hosts directory relies on inotify. The production build's compile-time
  options have not been re-read since the upgrade; the `alpine:3.23` package
  (`dnsmasq-2.91-r1`), the one an Alpine 3.23 host installs, lists
  `inotify`.
- Bearer-token auth cannot apply to the external-dns side. The external-dns
  v0.24.0 webhook client sends only the `Accept` and `Content-Type` headers,
  and has no flags for custom headers, client certificates or a custom CA.
  The auth choice for the webhook listener is an open decision for the
  service design.
- Change-set keys are lowercase camelCase. In external-dns v0.24.0 the
  `POST /records` body uses `create`, `updateOld`, `updateNew` and `delete`,
  not `Create`, `UpdateOld`, `UpdateNew` and `Delete`.
- TXT ownership records travel as ordinary endpoints: external-dns v0.24.0
  sends them through `POST /records` and expects them back from
  `GET /records`. They can therefore be held in the service's state and
  returned from `GET /records` without being published in DNS.
- Existing wildcard `address=` entries coexist with the service during
  rollout. On 2.91 a hosts-dir record takes precedence over a wildcard
  `address=` entry that also matches the name, and it is served as soon as
  inotify reads the file, with no reload
  (`TestHostsDirRecordBeatsWildcardWithoutReload`).
- Deletes do not need a reload on 2.91. The original says adds and updates
  are file writes only and deletes must also send SIGHUP; the probes on 2.91
  found that every hosts-directory change takes effect through inotify
  without a signal:
  - an added record is served at once
    (`TestHostsDirRecordBeatsWildcardWithoutReload`);
  - updating a record's address replaces the old address, so only the new
    one is served (`TestHostsDirUpdateDropsOldAddress`);
  - removing a record from the file stops it being served and the name falls
    back to any matching wildcard (`TestHostsDirRemovalDropsRecord`);
  - deleting the managed file drops its records too (checked by hand, not by
    a probe);
  - dnsmasq ignores dotfiles in the hosts directory, so writing a
    dot-prefixed temporary file and renaming it over the managed file never
    exposes a partial file.

  A SIGHUP after a write was verified harmless: the update and removal
  probes check the answer is unchanged one second after SIGHUP, and a manual
  run that signalled immediately after each write agrees.
  Whether the service still sends one as a belt-and-braces step is a
  service-design decision. dnsmasq before 2.88 did need SIGHUP for updates
  and removals, which is part of why older versions are not supported.
  Details are in [`docs/dnsmasq-findings.md`](../dnsmasq-findings.md).
- The full list of what proved wrong in the original, deliverable 4, is in
  [`docs/requirements-corrections.md`](../requirements-corrections.md). The
  ones not already above:
  - The webhook auth decision is made: its own plain-HTTP listener guarded
    by a source-IP allowlist. mTLS is impossible too, since external-dns
    v0.24.0 cannot present a client certificate.
  - external-dns v0.24.0 success codes are exact (204 for `POST /records`,
    200 for `GET /records` and `POST /adjustendpoints`), and any 4xx during
    a sync makes it exit; only 500 to 510 are retried.
  - Negotiation must return `{"include":[...]}`; an unknown key is ignored
    and filters nothing.
  - `/adjustendpoints` must drop what the service would refuse and return
    the rest normalised, not unchanged.
  - `--policy` has no default in v0.24.0 and must be set.
  - CNAME is unsupported in v1, and per-record TTLs are ignored: a hosts file
    holds neither.
  - The external-dns chart 1.24.0 always adds a webhook sidecar, so a
    remote webhook needs a post-render patch.

## Added by the owner

- A small read-only web page on port 80 showing the current DNS records and
  the last 50 API requests. This overrides "a UI" in the original's out of
  scope list.
- The aim is to retire the `lan-dns` repo. The service takes over what that
  repo does today, and the few hand-written rules that are still wanted move
  into the service's own config file.
- The DNS host was upgraded to Alpine 3.23 and dnsmasq 2.91 on 2026-11-17,
  and those are the minimum supported targets (owner's decision). The
  service and its tests need not handle older releases; see
  [Supported version](../dnsmasq-findings.md#supported-version).
- Standard library first. The service is expected to be built on `net/http`,
  `log/slog`, `html/template`, `encoding/json` and `testing`. Any third-party
  dependency needs a stated reason in the design doc that introduces it.

Open points the `lan-dns` aim raises for the service design:

- Whether the service renders the whole dnsmasq config or only the records.
- How hand-written rules such as wildcards and blocked top-level domains are
  expressed in the config file, given that the API itself refuses wildcards.
- The cutover steps that remove the symlink and the nightly pull.

Open points the web page raises for the service design:

- How an unprivileged process binds port 80 on Alpine.
- Whether the page needs any login on the LAN.
- Whether the request history is kept in memory only (lost on restart),
  given the "no separate database" requirement.

## Original requirements

The text below is the original as handed over, unedited.

# Build: an HTTP API for managing local dnsmasq records, with an external-dns webhook provider

## Goal
A small service that lets a Kubernetes cluster register and remove LAN-only DNS names in dnsmasq automatically. It has two faces:
1. A simple REST API for managing A/AAAA/CNAME records in dnsmasq.
2. An external-dns "webhook provider" API on top of it, so external-dns in the cluster can drive it without custom code on the cluster side.

## Environment
- dnsmasq runs in an LXC container on the LAN, outside the cluster. The service runs in the same container (it must write files dnsmasq reads).
- The cluster has a private ingress controller (Traefik, IngressClass `traefik-private`) on a single node at a fixed LAN IP. Every private hostname resolves to that one IP. Private hostnames have no public DNS record.
- Private hosts all live under one zone: <private-zone> (supplied as config; never hard-code it).
- Expected scale: tens of records, a handful of changes per week.

## How records must reach dnsmasq
- Use dnsmasq's `hostsdir=<dir>` (e.g. /etc/dnsmasq.hosts.d). dnsmasq watches that directory with inotify and loads new or changed files without a restart or SIGHUP.
- Known limitation to design around: with hostsdir, deleting a file or removing a line does NOT remove the record from dnsmasq's cache until it is reloaded. So adds/updates are file writes only; deletes must write the file and then send SIGHUP to dnsmasq (or `systemctl reload dnsmasq`). Verify this behaviour against the installed dnsmasq version and document what you found.
- A/AAAA records go in hosts-format files. CNAMEs cannot be expressed in a hosts file; either put them in a managed conf-dir file (`cname=`, needs a restart) or declare CNAME unsupported in v1. State which you chose and why.
- Write atomically (temp file in the same directory + rename), one managed file owned by this service, with a header saying it is generated. Never touch files it does not own.
- The service is the source of truth for its file: keep state in the file itself (parse on start), no separate database.

## REST API (management)
- GET /v1/records -> list {name, type, value, ttl?}.
- PUT /v1/records/{name} -> create or replace (idempotent).
- DELETE /v1/records/{name} -> remove (idempotent; 204 if absent).
- GET /healthz -> 200 when the hosts dir is writable and dnsmasq is running.
- Validation: names must be valid hostnames inside the configured allowed zone(s); values must be valid IPs inside configured allowed CIDRs (default RFC1918 only). Reject anything else with 422. This is the main safety control: the API must be incapable of pointing an arbitrary name at an arbitrary address.
- No wildcard records through the API.

## external-dns webhook provider API
Implement the external-dns webhook provider contract (see the external-dns docs, "Webhook provider" tutorial; check the current version of the contract before coding - do not rely on this summary alone):
- GET / -> negotiation; returns the domain filter (the private zone). Media type `application/external.dns.webhook+json;version=1` for requests and responses.
- GET /records -> current endpoints as a JSON array of {dnsName, targets[], recordType, recordTTL?, labels?, providerSpecific?}.
- POST /records -> body {Create[], UpdateOld[], UpdateNew[], Delete[]}; apply and return 204. Must be idempotent and apply the whole change set or none of it.
- POST /adjustendpoints -> return the endpoints unchanged (or normalised).
- external-dns normally expects the webhook as a sidecar on localhost. Here it is remote, called over the LAN via --webhook-provider-url. That makes authentication and TLS your responsibility (below).
- Ownership: support external-dns's TXT registry. dnsmasq can serve TXT via `txt-record=` in a conf file, which needs a restart, so the pragmatic option is to store TXT ownership records in the service's own state and return them from GET /records without publishing them in DNS. Alternatively document that external-dns must run with --registry=noop. Pick one, justify it.

## Security
- Listen only on the LAN interface; never on a public or wildcard address unless configured.
- Bearer-token auth on every endpoint except /healthz. Token from an env var or a root-only file, compared in constant time. Note that external-dns's webhook client may not be able to send custom auth headers - check the current version; if it cannot, put the service behind a reverse proxy that enforces mTLS or an IP allowlist limited to the cluster nodes, and document the chosen approach.
- TLS: accept a cert/key path; plain HTTP only if explicitly enabled.
- Run as an unprivileged user with write access to the hosts dir only, plus the narrowest possible way to signal dnsmasq (a sudoers rule for one command or a systemd ExecReload hook).
- Log every mutation (who, what, before/after) to stdout/journald.

## Non-functional
- Single static binary (Go preferred) or a single-file service with minimal dependencies; systemd unit included; config via env vars or one config file.
- Survives restart with no loss of state; safe under concurrent requests (serialise writes).
- Unit tests for validation, file rendering/parsing round-trip, and the change-set logic; an integration test that runs a real dnsmasq against a temp hostsdir and asserts with dig that add, update and delete take effect.

## Deliverables
1. Source, tests, systemd unit, example config.
2. README: install in the LXC, the dnsmasq config lines required (hostsdir=...), how to rotate the token, how to roll back (delete the managed file + reload).
3. The exact external-dns arguments the cluster side needs, as a values snippet: --provider=webhook, --webhook-provider-url=..., --source=ingress, --ingress-class=traefik-private, --domain-filter=<private-zone>, registry and policy flags matching your ownership decision. Targets come from the Ingress status address, which the private ingress controller already publishes as its node's LAN IP.
4. A short list of anything in this spec that turned out to be wrong or unsupported when checked against the real dnsmasq / external-dns versions.

## Out of scope
- Public DNS, DHCP management, a UI, multi-tenant access control.
- Deploying external-dns into the cluster (that is done from the GitOps repo using your values snippet).

## Open questions to answer before building
- Which dnsmasq version is installed, and does delete-without-reload behave as described?
- Does the installed external-dns version allow auth headers or client certs on the webhook call?
- Is CNAME support needed in v1?
