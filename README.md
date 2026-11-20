# dnsmasq-manager

dnsmasq-manager is a small Go service that manages LAN-only DNS records in
dnsmasq. It offers a REST API for records and an external-dns webhook
provider on top, so a Kubernetes cluster can register and remove private
hostnames itself. It runs on the DNS host beside dnsmasq and writes one file
in dnsmasq's hosts directory.

Go module: `github.com/gsdevme/dnsmasq-manager`.

## Status

The `serve` command is written (issue #4): REST API, webhook provider and a
read-only web page over one managed hosts file. The OpenRC service, its user
and the capability to bind port 80 come with issue #6; until then the
service is run by hand (see [Running the service](#running-the-service)).
What exists today:

- the service requirements, corrected against the DNS host, dnsmasq 2.91 and
  external-dns v0.24.0, and the
  [list of what proved wrong](docs/requirements-corrections.md);
- a command line on [cobra](https://github.com/spf13/cobra)
  (`cmd/dnsmasq-manager`, `internal/`) with two commands, `serve` and
  `version`; cobra is the one third-party dependency;
- releases published by release-please, each with a static `linux/amd64`
  binary, its SHA-256 checksum and an installer for Alpine (see
  [Installation](#installation) and [Releasing](#releasing));
- dnsmasq behaviour probes in `test/integration/`, run against a mock config
  in `testdata/dnsmasq/dnsmasq.conf`, and their results, plus tests of the
  real binary beside dnsmasq 2.91;
- an end-to-end harness (`make e2e`) running real external-dns v0.24.0
  against the service;
- a session-start hook that prepares Claude Code cloud sessions;
- hooks and a `project-manager` agent that keep the roadmap issues in step
  with the work (see
  [How issues stay in step with the work](#how-issues-stay-in-step-with-the-work)).

The hook has not yet been tried in a real cloud session, so whether the
plugin it installs is usable there is not yet verified (see below).

## Installation

The installer puts the binary on an Alpine host, and nothing else: no
service, user or config file until issue #6. To run it, see
[Running the service](#running-the-service).

Requirements: Alpine Linux 3.23 or later, `x86_64`, and root.

The quick form downloads the installer from the latest release and pipes it
to `sh`:

```sh
wget -qO- https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh | sh
```

In this form a failed download of the script itself still exits 0, because
`sh` reads nothing and succeeds, so check for the `installed version` line
or use the form below when the exit status matters.

The safer form downloads it first, so you can read it before running it:

```sh
wget -O install.sh https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh
less install.sh
sh install.sh
```

To install a specific release rather than the latest, set
`DNSMASQ_MANAGER_VERSION`. `0.1.0` and `v0.1.0` mean the same release:

```sh
DNSMASQ_MANAGER_VERSION=0.1.0 sh install.sh
```

The script reads its settings from the environment:

| Variable | Default | Meaning |
| --- | --- | --- |
| `DNSMASQ_MANAGER_VERSION` | `latest` | `latest`, or a release such as `v0.1.0`; anything else is refused before any download |
| `DNSMASQ_MANAGER_BASE_URL` | `https://github.com/gsdevme/dnsmasq-manager/releases` | Where releases are downloaded from; the tests point it at a local server |
| `DNSMASQ_MANAGER_INSTALL_DIR` | `/usr/local/bin` | Where the binary goes; must already exist and be writable |
| `DNSMASQ_MANAGER_OS_RELEASE` | `/etc/os-release` | The file read for the OS check; a test seam |

What the script does, in order:

1. Checks that it runs as root, on `x86_64`, on Alpine 3.23 or later (read
   from `os-release` without sourcing it), that the version is safe to put
   in a URL, that the install directory exists and is writable, and that
   `dnsmasq-manager` in it is not a directory. Any failed check exits 1
   before anything is downloaded.
2. Downloads `dnsmasq-manager-linux-amd64` and
   `dnsmasq-manager-linux-amd64.sha256` into a temporary directory.
3. Refuses a checksum file that is not exactly one well-formed line, then
   verifies the binary with `sha256sum -c`.
4. Copies the binary to a `mktemp` staging file in the install directory,
   sets mode `0755`, runs it with `version`, and only then renames it over
   `dnsmasq-manager`, so the swap is atomic.
5. Prints the previous version (`none` on a first install) and the version
   the new binary reports.

Messages start with `dnsmasq-manager install:`; errors go to stderr. A
failed check, download, checksum or run of the new binary leaves any
installed binary as it was,
and the temporary directory and staging file are removed on every exit.
Running the script again reinstalls or upgrades in place.

What the script does not do: it changes nothing but that one file. It
creates no service, user or config file, and does not touch dnsmasq or its
settings.

To uninstall, remove the binary:

```sh
rm /usr/local/bin/dnsmasq-manager
```

## Running the service

`dnsmasq-manager serve` takes no flags; it reads its configuration from
`DNSMASQ_MANAGER_*` environment variables. It loads the managed file, binds
its listeners, logs one `listening` line per listener and serves until
SIGINT or SIGTERM, when it gives in-flight requests five seconds and exits
0. Any start-up problem is printed as `dnsmasq-manager: <error>` on stderr
before anything is served, and the exit status is 1. Logs are `log/slog`
text lines on stdout; every change to a record is logged with the listener,
the caller's address, the name, the type and the addresses before and
after. The token is never logged.

One process, three listeners, one shared set of records:

- **REST API** (`API_ADDR`): for a person or a script to manage A and AAAA
  records. TLS unless plain HTTP is chosen, bearer token on every route but
  `/healthz`.
- **external-dns webhook** (`WEBHOOK_ADDR`): the external-dns v0.24.0
  webhook provider protocol, plain HTTP behind a source-IP allowlist.
- **Web page** (`WEB_ADDR`): read-only, no login. Health, the records, and
  the last 50 recorded requests (every REST call except `/healthz`; webhook
  change sets and errors), kept in memory and lost on restart. It reloads
  itself every 30 seconds.

The records live in `<HOSTS_DIR>/dnsmasq-manager.hosts`, which is also the
service's only state: no database. Each change is written to a dot-prefixed
temporary file and renamed into place, so dnsmasq never reads a partial
file. Besides the address lines dnsmasq serves, the file holds `#@` lines
dnsmasq treats as comments: who owns each name (`api` or `external-dns`)
and external-dns's TXT ownership records, which are kept as state and never
published in DNS. The file is generated: do not edit it while the service
runs (the next write overwrites the edit) and expect a hand-edited file to
be refused at the next start (see
[When start-up refuses the managed file](#when-start-up-refuses-the-managed-file)).

### Configuration

Every variable starts with `DNSMASQ_MANAGER_`. All problems are reported
together, one line each, naming the variable. A variable set to the empty
string counts as unset, so it takes the default. The installer's own
variables (`DNSMASQ_MANAGER_VERSION`, `_BASE_URL`, `_INSTALL_DIR` and
`_OS_RELEASE`, see [Installation](#installation)) are ignored, so `serve` runs in the
shell they were exported in. Any other `DNSMASQ_MANAGER_*` variable is
refused, to catch typos. An annotated example
is in [`docs/examples/dnsmasq-manager.conf.d`](docs/examples/dnsmasq-manager.conf.d).

| Variable | Default | Meaning |
| --- | --- | --- |
| `DNSMASQ_MANAGER_ZONES` | required | Comma-separated zones records may live in, such as `home.example.test`. A record must be a strict subdomain of one; the zone itself is refused. Each zone needs at least two labels; it is stored lowercase without a trailing dot |
| `DNSMASQ_MANAGER_ALLOWED_CIDRS` | `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16` | Prefixes every record address must fall inside. AAAA records need an IPv6 prefix added here. A prefix with host bits set is refused |
| `DNSMASQ_MANAGER_HOSTS_DIR` | `/etc/dnsmasq.hosts.d` | dnsmasq's `hostsdir`; must exist and be a directory |
| `DNSMASQ_MANAGER_DNS_PROBE_ADDR` | `127.0.0.1:53` | Where `/healthz` asks dnsmasq a question |
| `DNSMASQ_MANAGER_API_ADDR` | `127.0.0.1:8443` | REST listener |
| `DNSMASQ_MANAGER_API_TOKEN` | | The bearer token, at least 32 characters, with no whitespace or control characters. Set exactly one of this and `API_TOKEN_FILE` |
| `DNSMASQ_MANAGER_API_TOKEN_FILE` | | A file holding the token. Trailing line endings are trimmed; the file is refused if group or others can read it |
| `DNSMASQ_MANAGER_API_TLS_CERT`, `DNSMASQ_MANAGER_API_TLS_KEY` | | PEM certificate and key for the REST listener (TLS 1.2 or later); both or neither |
| `DNSMASQ_MANAGER_API_PLAIN_HTTP` | `false` | `true` serves REST without TLS. Set exactly one of this and the certificate pair |
| `DNSMASQ_MANAGER_WEBHOOK_ADDR` | `127.0.0.1:8888` | Webhook listener, or `off` |
| `DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES` | `127.0.0.1/32` | Comma-separated IPs or prefixes allowed to call the webhook |
| `DNSMASQ_MANAGER_WEB_ADDR` | `127.0.0.1:80` | Web page listener, or `off` |
| `DNSMASQ_MANAGER_ALLOW_WILDCARD_BIND` | `false` | `true` permits `0.0.0.0` or `::` in a listener address |
| `DNSMASQ_MANAGER_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

Listener addresses are a literal IP and port, never a host name, and the
enabled listeners must use different addresses. Booleans are exactly `true`
or `false`. The token and the certificate are read once, at start-up.

### dnsmasq configuration

dnsmasq needs one line in its config, naming the same directory as
`DNSMASQ_MANAGER_HOSTS_DIR`:

```
hostsdir=/etc/dnsmasq.hosts.d
```

dnsmasq 2.91 watches that directory with inotify. Every change the service
makes, including a removal and deleting the file, is served as soon as
dnsmasq reads it: no reload, no SIGHUP, and the service needs neither
dnsmasq's PID nor permission to signal it. A record in the hosts directory
takes precedence over a wildcard `address=` entry for the same name. The
build must include `inotify` (`dnsmasq --version` lists it; Alpine's package
does). The evidence is in [`docs/dnsmasq-findings.md`](docs/dnsmasq-findings.md).

### Running it before the installer work in #6

Until #6 there is no init script, service user or conf.d file, so the
service is run by hand in a shell on the DNS host. Create the hosts
directory and a token first:

```sh
mkdir -p /etc/dnsmasq.hosts.d /etc/dnsmasq-manager
(umask 077; head -c 32 /dev/urandom | base64 > /etc/dnsmasq-manager/api-token)
```

Then set the variables (or source an edited copy of the example conf.d
file) and run it:

```sh
export DNSMASQ_MANAGER_ZONES=home.example.test
export DNSMASQ_MANAGER_API_TOKEN_FILE=/etc/dnsmasq-manager/api-token
export DNSMASQ_MANAGER_API_TLS_CERT=/etc/dnsmasq-manager/api.crt
export DNSMASQ_MANAGER_API_TLS_KEY=/etc/dnsmasq-manager/api.key
export DNSMASQ_MANAGER_WEBHOOK_ADDR=10.0.0.53:8888
export DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES=10.0.0.21,10.0.0.22
export DNSMASQ_MANAGER_WEB_ADDR=10.0.0.53:8080
dnsmasq-manager serve
```

A listener that cannot bind stops start-up, and an unprivileged process
cannot bind port 80 until #6 grants the capability. So a non-root run sets
`DNSMASQ_MANAGER_WEB_ADDR` to a high port, as above, or to `off`. The user
running it also needs write access to the hosts directory and read access
to the token file, which must not be readable by group or others.

### REST API

JSON in and out. Every route except `/healthz` needs
`Authorization: Bearer <token>`; without it, or with the wrong token, the
reply is 401 with `WWW-Authenticate: Bearer`. Errors are
`{"error":"<message>"}`. The examples assume:

```sh
API=https://10.0.0.53:8443        # http://... with DNSMASQ_MANAGER_API_PLAIN_HTTP=true
AUTH="Authorization: Bearer $(cat /etc/dnsmasq-manager/api-token)"
```

Add `--cacert <file>` when the certificate is not signed by a CA the client
trusts.

Health, no token. 200 when both checks pass, otherwise 503 with
`"status":"unhealthy"` and the failing check's error in place of `ok`.
`hosts_dir` checks the process can create files in the hosts directory;
`dnsmasq` checks dnsmasq answers a `version.bind` query at
`DNSMASQ_MANAGER_DNS_PROBE_ADDR` within one second:

```sh
curl -sS "$API/healthz"
# 200 {"status":"ok","checks":{"hosts_dir":"ok","dnsmasq":"ok"}}
```

List every A and AAAA record, sorted by name then type (TXT ownership
records are not listed):

```sh
curl -sS -H "$AUTH" "$API/v1/records"
# 200 {"records":[{"name":"nas.home.example.test","type":"A","values":["10.0.0.40"],"source":"api"}]}
```

Set one name's record of one type; the other type of the same name is left
alone. `type` is `A` or `AAAA`, in capitals. `values` is a list of 1 to 16
addresses, deduplicated and sorted.
`ttl` is optional, must be a non-negative integer, and is ignored. The name
is lowercased and one trailing dot removed. Setting the same thing twice is
the same as once:

```sh
curl -sS -X PUT -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"type":"A","values":["10.0.0.40"]}' \
  "$API/v1/records/nas.home.example.test"
# 200 {"name":"nas.home.example.test","type":"A","values":["10.0.0.40"],"source":"api"}
```

Delete every record of a name, or only one type with `?type=A` or
`?type=AAAA`. 204 whether or not anything was there:

```sh
curl -sS -X DELETE -H "$AUTH" "$API/v1/records/nas.home.example.test"
curl -sS -X DELETE -H "$AUTH" "$API/v1/records/nas.home.example.test?type=AAAA"
# 204
```

A name external-dns created has source `external-dns`, and a `PUT` or
`DELETE` on it is refused with 409 unless `?force=true` is given:

```sh
curl -sS -X PUT -H "$AUTH" -d '{"type":"A","values":["10.0.0.81"]}' \
  "$API/v1/records/app.home.example.test"
# 409 {"error":"conflict: app.home.example.test is managed by external-dns; add ?force=true to take it over"}
curl -sS -X PUT -H "$AUTH" -d '{"type":"A","values":["10.0.0.81"]}' \
  "$API/v1/records/app.home.example.test?force=true"
# 200 {"name":"app.home.example.test","type":"A","values":["10.0.0.81"],"source":"api"}
```

`force` takes the name over: the change is applied, the name's source
becomes `api`, and the TXT ownership records external-dns keeps for that
name are dropped. external-dns then sees a record it does not own and
leaves it alone, even while an Ingress still asks for the name. On `DELETE`
the records and those TXT records go together. To hand a name back to
external-dns, delete it; external-dns creates it again at its next sync.

| Status | When |
| --- | --- |
| 200 | `GET /healthz` healthy, `GET /v1/records`, a successful `PUT` |
| 204 | A successful `DELETE` |
| 400 | Malformed JSON, an unknown field (such as `value` for `values`), an empty body, a bad query parameter (`force` other than `true` or `false`, `type` that is not an upper-case type name, either given twice), or a query parameter the route does not take (`DELETE` takes `force` and `type`, `PUT` takes `force`, the `GET` routes take none; names are case-sensitive, so `?Type=AAAA` is refused, not ignored) |
| 401 | Missing or wrong token |
| 404, 405 | Unknown route or method |
| 409 | The name's source is `external-dns` and `?force=true` was not given |
| 413 | Body over 64 KiB |
| 422 | Validation failure: a name outside the zones, the zone itself, a wildcard or underscore, an address outside `ALLOWED_CIDRS` or of the wrong family, no or more than 16 addresses, a negative `ttl`, or an unsupported type such as `CNAME` or `MX` |
| 500 | The hosts file could not be written; the records and the file are unchanged |
| 503 | `GET /healthz` unhealthy |

### external-dns

external-dns runs in the cluster and calls the webhook on the DNS host over
the LAN; the webhook is remote, not a sidecar in the external-dns pod. The
arguments it needs:

```
--provider=webhook
--webhook-provider-url=http://<dns-host>:8888
--source=ingress
--ingress-class=traefik-private
--domain-filter=<private-zone>
--registry=txt
--txt-owner-id=<id>
--policy=sync
```

`--policy` must be set: external-dns v0.24.0 has no default. The Helm values
for these, checked against the external-dns chart 1.24.0, are in
[`docs/examples/external-dns-values.yaml`](docs/examples/external-dns-values.yaml).
That chart always adds a sidecar container when the provider is `webhook`,
so the file also shows the post-render patch that removes it.

The webhook listener is plain HTTP, and its only guard is the source-IP
allowlist in `DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES`: a caller whose
connection address is outside it gets 403, and forwarding headers are
ignored. It has no token because external-dns v0.24.0 cannot send one: its
webhook client sends only `Accept` and `Content-Type`, with no flags for
custom headers, client certificates or a custom CA. List the addresses the
DNS host sees the external-dns pod's traffic come from, usually the cluster
nodes' LAN addresses. Refused calls appear on the web page's request list
with the caller's address, which shows what to add.

What external-dns gets:

- Negotiation returns the zones (`{"include":[...]}`), so external-dns only
  plans names inside them.
- Records created through the REST API are returned to external-dns too, so
  it sees the name is taken and does not try to create it. A change set
  that would touch such a name fails whole with 500; with the TXT registry
  external-dns never sends one, so it points at a misconfiguration such as
  `--registry=noop`.
- An endpoint the service would refuse (a CNAME, a name outside the zones,
  an address outside the allowed prefixes) is dropped in
  `/adjustendpoints`, so one bad Ingress does not fail the sync for the
  others. The dropped list is logged and shown on the web page when it
  changes, tracked per calling address so two external-dns instances with
  different dropped lists each report only their own changes.
- external-dns exits on any 4xx during a sync, so the webhook answers every
  failure with 500, which external-dns retries at the next sync. The only
  4xx replies are the allowlist's 403, 404 or 405 for unknown routes, and
  415 for a `POST` whose `Content-Type` is not
  `application/external.dns.webhook+json;version=1`. external-dns always
  sends that header, so the 415 only refuses callers that are not
  external-dns, such as a browser page posting cross-origin.

### Rotating the token

Write the new token, at least 32 characters, to the token file (or change
`DNSMASQ_MANAGER_API_TOKEN`), then restart the service. The token is read
only at start-up, and there is no reload. The old token stops working at
the restart, so update every REST client with the new one. The webhook has
no token and is not affected.

```sh
(umask 077; head -c 32 /dev/urandom | base64 > /etc/dnsmasq-manager/api-token)
```

### When start-up refuses the managed file

At start-up the service reads `dnsmasq-manager.hosts`, checks every record
against the current zones and prefixes, and requires the file to be exactly
what it would write itself. If not, it refuses to start, leaves the file
alone, and names the file, the line and the reason:

```
dnsmasq-manager: /etc/dnsmasq.hosts.d/dnsmasq-manager.hosts:3: record app.home.example.test A: invalid: 10.0.0.81 is outside the allowed prefixes
dnsmasq-manager: /etc/dnsmasq.hosts.d/dnsmasq-manager.hosts:5: comment lines are not allowed
```

Line `0` means the file as a whole, as in `...hosts:0: not in canonical form`.
dnsmasq keeps serving the file meanwhile. There is no repair command; the
way out is one of:

- **Put the configuration back.** If a zone or prefix was removed from
  `DNSMASQ_MANAGER_ZONES` or `DNSMASQ_MANAGER_ALLOWED_CIDRS`, restore it and
  start again.
- **Remove the named entry.** Delete the whole entry the error points at: a
  `#@ name` line together with the address lines under it, or a single
  `#@ txt` line. Leave no blank line. A file with whole entries removed is
  still in canonical form, so it loads. A hand edit inside an entry (an
  added comment, a reordered address) is undone the same way, or by
  restoring the file from a backup.

The error names one problem at a time, so start again after each fix. A
missing file is an empty state, so deleting the file is the last resort; it
also removes every record at once.

### Rolling back

1. Stop external-dns first, or scale it to zero, if it is pointed at the
   webhook: a refused connection is a hard error that makes it exit and
   its pod restart.
2. Stop the service.
3. Delete the managed file:

   ```sh
   rm /etc/dnsmasq.hosts.d/dnsmasq-manager.hosts
   ```

dnsmasq 2.91 drops the file's records as soon as it sees the deletion, with
no reload, and the names fall back to whatever else answers them, such as a
wildcard `address=` entry. Nothing else in the hosts directory is the
service's. Removing the `hostsdir=` line too is a change to the main
config; how dnsmasq picks that up was not probed, so restart dnsmasq after
it.

## Building and testing

Needs Go 1.27 (from `go.mod`). Lint needs golangci-lint at the version pinned
in the `Makefile`; `make tools` installs it with `go install`. The integration
probes need either Linux with `dnsmasq` 2.91 or later and `dig`, or Docker;
the end-to-end harness needs Docker.

| Target | What it does |
| --- | --- |
| `make build` | Static `linux/amd64` binary at `bin/dnsmasq-manager`, with the version from `git describe` |
| `make dist` | The release files in `dist/`: the binary as `dnsmasq-manager-linux-amd64`, its `.sha256` checksum and `install.sh`; `VERSION=v0.1.0` stamps the version |
| `make test` | Unit tests with `-race` |
| `make lint` | `golangci-lint run ./...` |
| `make tools` | Installs the pinned golangci-lint |
| `make test-hook` | Tests the Claude Code hooks (session start, project manager and pull request review) against stub commands; needs only bash |
| `make test-install` | Tests `install.sh` against stub commands; needs bash, and runs the installer under `TEST_SH` (default `/bin/dash`) |
| `make test-install-docker` | Runs `install.sh` end to end in `alpine:3.23` against the real binary served over local HTTP, then the stub tests under BusyBox ash; needs Docker |
| `make integration` | Integration probes against a real `dnsmasq`; Linux only, with `dnsmasq` 2.91 or later and `dig` on PATH |
| `make integration-docker` | The same probes in `alpine:3.23` with dnsmasq 2.91, the production release and version and the supported minimums; use this on macOS |
| `make e2e` | Real external-dns v0.24.0 against the service beside dnsmasq 2.91, with a throwaway Kubernetes API for it to read Ingresses from; needs Docker |

## Releasing

Releases come from Conventional Commits on `main`, through
[release-please](https://github.com/googleapis/release-please) in
`.github/workflows/release.yml`. Its settings are in
`release-please-config.json`.

- Each push to `main` opens or updates a release pull request titled
  `build: release <version>`, with the next version and the changelog.
  Before 1.0 a breaking change bumps the minor version.
- Merging that pull request tags `v<version>` and creates the GitHub
  release. A second job in the same workflow checks out the tag, runs
  `make dist VERSION=<tag>` and uploads the three files:
  `dnsmasq-manager-linux-amd64`, `dnsmasq-manager-linux-amd64.sha256` and
  `install.sh`.
- Known gap: between the release being created and the upload finishing,
  the latest release has no assets, so an install in that window fails at
  download and changes nothing. Run it again once the assets are there.
- If the `assets` job fails, re-run that job on its own; the upload uses
  `--clobber`, so overwriting files from a partial run is safe.

One-time setup by the repository owner:

1. In the repository's Actions settings, enable "Allow GitHub Actions to
   create and approve pull requests", so release-please can open its pull
   request.
2. Make the repository public. The installer downloads release assets
   without a token, which only works for a public repository.

After a release, check that it has the three assets, then smoke-test it in
a throwaway container, never on the live DNS host:

```sh
docker run --rm --platform linux/amd64 alpine:3.23 sh -c 'wget -qO- https://github.com/gsdevme/dnsmasq-manager/releases/latest/download/install.sh | sh && dnsmasq-manager version'
```

## Claude Code sessions

Work in this repo follows the workflow in [`CLAUDE.md`](CLAUDE.md):
requirements, design doc, plan, test-driven implementation, review, using the
superpowers plugin when it is loaded.

`.claude/settings.json` enables `superpowers@claude-plugins-official` for
local sessions and registers `.claude/hooks/session-start.sh` as a
`SessionStart` hook on startup and resume. Cloud sessions ignore
`enabledPlugins`, so the hook installs what a cloud session needs:

- Outside a cloud session (`CLAUDE_CODE_REMOTE` is not `true`) it exits at
  once and prints nothing.
- In a cloud session it installs the superpowers plugin from the
  `anthropics/claude-plugins-official` marketplace if `claude plugin list`
  does not already show it; installs `dnsmasq-base` and `dnsutils` with
  `apt-get` if `dnsmasq` or `dig` is missing (as root or with passwordless
  `sudo`, otherwise it skips them with a note); runs `make tools` if
  golangci-lint is missing; runs `go mod download`; and adds the Go bin
  directory to the session's PATH.
- It then prints a short status block (Go version, whether `dnsmasq`, `dig`,
  golangci-lint and superpowers are present) that becomes session context.
- Anything already present is left alone. Every step tolerates failure and
  the hook always exits 0, so a blocked download never stops the session.

Known limit: Claude Code loads plugins when a session starts, so a plugin the
hook installs during that start may only be usable after `/reload-plugins` or
in a second session. This is pending the cloud test, whose results go in
[`docs/cloud-session-findings.md`](docs/cloud-session-findings.md). Until
superpowers is loaded, follow the workflow in `CLAUDE.md` by hand.

## How issues stay in step with the work

The roadmap is in GitHub issues: an epic and one issue per phase, each with
a task list and exit criteria. Hooks and an agent keep them current, so
nobody edits them by hand.

- A branch named `<type>/<issue>-<slug>`, such as `feat/4-serve`, belongs to
  that issue. Where the name cannot be chosen, as in a cloud session,
  `bash .claude/hooks/pm-issue.sh link <issue>` records the link instead.
- When work starts on a linked branch, `.claude/hooks/pm-start.sh` gives the
  session the issue's title, state, URL and body, once per session and
  branch. The body is shown only when a collaborator wrote it. On any other
  branch the session is told once how to link one.
- When a turn ends with pushed commits that have not been synced,
  `.claude/hooks/pm-stop.sh` holds the session open once and asks it to
  dispatch the `project-manager` agent (`.claude/agents/project-manager.md`).
- The agent ticks a checklist item only when a commit on the branch completes
  it, and keeps one progress comment per branch that names the commit behind
  each ticked item. It never closes, creates or relabels an issue.
- A pull request carries `Closes #<issue>`, so the issue closes on merge and
  the epic's task list follows.

All GitHub calls go through `.claude/hooks/pm-issue.sh`, over REST with
`gh api`, which also works in cloud sessions. State is kept per clone in
`.git/project-manager/`. Not yet tried on a real issue: the first roadmap
branch is the trial, and the design lists what it has to settle.

## Automatic review of pull requests

A pull request opened by a local Claude Code session is reviewed by a
second Claude Code run that has not seen the session's conversation.

- `.claude/hooks/pr-review.sh` runs after each shell command. When the
  command opened a pull request (`gh pr create`, or a REST `POST` to
  `/pulls`), it asks GitHub for the open pull request on the current branch
  and starts `claude -p "/code-review medium <n>"` in the background. The
  session is told in one line and carries on.
- The review run only reads and reports. Its tools are read-only, the
  writing ones (`Edit`, `Write`, `gh api`, `git push`) are denied by name,
  and this repository's hooks are turned off inside it.
- When the run finishes, the hook posts its findings as one comment on the
  pull request, under the `gh` login of whoever runs the session. The
  comment names the commit reviewed and lists each finding with its file
  and line (rendered with `jq` when it is installed). Output that is not a
  list of findings is not posted.
- The checkout's own path is made repo-relative in the comment. Findings
  that still hold a local path, a private address or a token are not
  posted; the log says why and the findings stay on disk.
- Each pull request is reviewed once. State is kept per clone in
  `.git/pr-review/`: `reviewed-<n>`, the run's output in `review-<n>.md`,
  the comment body in `review-<n>.body.md` and the log in
  `review-<n>.log`. A run that fails, prints nothing or
  cannot post removes its `reviewed-<n>` file and says so in the log. To
  review again after more commits, run `/code-review <n>` in a session.
- `PR_REVIEW_EFFORT` sets the review's effort level: `low`, `medium` (the
  default), `high` or `xhigh`.

Not covered: pull requests opened in the browser, by release-please or from
a cloud session.

## Documents

- [Service requirements](docs/requirements/dnsmasq-api.md): the input to the
  service design, and [what in them proved wrong](docs/requirements-corrections.md)
  against dnsmasq 2.91 and external-dns v0.24.0.
- [`serve` design](docs/superpowers/specs/2026-11-19-serve-design.md) and
  [plan](docs/superpowers/plans/2026-11-19-serve.md): the service itself.
- Examples: an [OpenRC conf.d environment](docs/examples/dnsmasq-manager.conf.d)
  and [external-dns Helm values](docs/examples/external-dns-values.yaml).
- [dnsmasq findings](docs/dnsmasq-findings.md): what the probes observed on
  dnsmasq 2.91, the minimum supported version, and why older ones are not
  supported.
- [Cloud session findings](docs/cloud-session-findings.md): the cloud test
  checklist, not yet answered.
- [Project-manager agent design](docs/superpowers/specs/2026-11-17-project-manager-agent-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-project-manager-agent.md): the
  hooks and agent that keep issues in step with the work.
- [Pull request review hook design](docs/superpowers/specs/2026-11-19-pr-review-hook-design.md)
  and [plan](docs/superpowers/plans/2026-11-19-pr-review-hook.md): the hook
  that starts a fresh-context review of each pull request a session opens.
  [Amended](docs/superpowers/specs/2026-11-19-pr-review-single-comment-design.md)
  ([plan](docs/superpowers/plans/2026-11-19-pr-review-single-comment.md))
  so that the hook, not the review run, posts the findings.
- [Release path design](docs/superpowers/specs/2026-11-17-release-path-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-release-path.md): the CLI on
  cobra, `make dist`, the installer and the release workflow.
- [Foundation design](docs/superpowers/specs/2026-11-17-agentic-repo-foundation-design.md)
  and [plan](docs/superpowers/plans/2026-11-17-agentic-repo-foundation.md):
  how this repository was set up.
