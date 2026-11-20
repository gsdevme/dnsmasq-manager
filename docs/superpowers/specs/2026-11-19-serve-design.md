# The `serve` command: the HTTP API service

Date: 2026-11-20
Status: approved 2026-11-20
Issue: #4 (first phase of epic #3)

Input: [`docs/requirements/dnsmasq-api.md`](../../requirements/dnsmasq-api.md),
whose "Corrections" and "Added by the owner" sections win over the original
text, read with [`docs/dnsmasq-findings.md`](../../dnsmasq-findings.md).

## Intent

A Kubernetes cluster should be able to register and remove LAN-only DNS names
in dnsmasq without anyone editing a file. Today the binary only prints its
version. This change adds `dnsmasq-manager serve`: one process on the DNS host
with three faces.

1. A REST API for a person or a script to manage A and AAAA records.
2. An external-dns webhook provider, so external-dns v0.24.0 in the cluster
   drives the same records with no custom code on the cluster side.
3. A small read-only web page showing the records and recent requests.

The main safety property, from the requirements: the service must be incapable
of pointing an arbitrary name at an arbitrary address.

## Goals

1. `dnsmasq-manager serve` runs against dnsmasq 2.91 on Alpine 3.23; a record
   added, changed or removed through either API is what `dig` sees, with no
   reload.
2. State survives a restart with no database: the managed hosts file is the
   state.
3. external-dns v0.24.0 with its TXT registry works against the webhook
   without error loops or needless updates.
4. `make test`, `make lint`, `make build` (still static),
   `make integration-docker` and `make e2e` pass.
5. No new third-party dependency.

## Non-goals

- Hand-written dnsmasq rules such as wildcards and blocked TLDs (#5).
- The service user, OpenRC script, installed conf.d file and the capability
  to bind port 80 unprivileged (#6). This change ships an example conf.d file
  as documentation only.
- CNAME records, TXT records published in DNS, per-record TTLs.
- A login on the web page, persistent request history, metrics.
- Reloading the token or certificate without a restart.

## Anti-goals

- A write that leaves dnsmasq serving a partial or mixed file.
- Overwriting a managed file the service cannot fully account for.
- Answering external-dns with a 4xx during a sync: in v0.24.0 that makes
  external-dns exit.
- Any test or check that touches the live DNS host.

## Constraints

- dnsmasq 2.91 and Alpine 3.23 are the minimums; nothing for older releases.
- Standard library first (`net/http`, `log/slog`, `html/template`,
  `encoding/json`); cobra is already present for the command tree.
- No private zone, LAN address or token in the repo. Examples use
  `home.example.test` and `10.0.0.0/24`.
- external-dns v0.24.0 facts below were read from its source at that tag
  (see [What external-dns v0.24.0 does](#what-external-dns-v0240-does)).

## Decisions made by the owner

- One design doc and one pull request for all of #4, on `feat/4-serve`.
- The webhook has its own listener, plain HTTP, guarded by a source-IP
  allowlist. The REST API is a separate listener with a bearer token on
  everything but `/healthz`.
- Configuration is by environment variables.
- The web page has no login, its history is memory-only, and its address is
  configurable with default port 80.

Everything else in this document is a proposal for the owner to confirm by
approving it. The ones most worth a look are listed under
[Calls to check](#calls-to-check).

## Approaches

How records and the service's own bookkeeping reach disk.

**A. One managed hosts file that carries everything (chosen).** Address lines
dnsmasq reads, plus `#@` comment lines dnsmasq ignores that hold each name's
source and external-dns's TXT ownership records. One atomic rename covers a
whole change, so records and ownership can never disagree after a crash.
Costs a small strict parser.

**B. A hosts file plus a JSON state file beside it.** Simpler parsing, but two
files cannot be replaced in one step: a crash between the two renames leaves
ownership and records out of step, and the JSON file would have to live
outside the hosts directory or be dot-prefixed so dnsmasq does not read it.

**C. A conf-dir file of `host-record=`, `cname=` and `txt-record=` lines.**
Would give CNAME and published TXT, but changes outside the hosts directory
need a dnsmasq restart or reload that has not been probed, and the service
would need permission to signal dnsmasq. The requirements do not need CNAME.

A is chosen: it is the only one where a write is complete at the rename, which
is what the 2.91 probes verified, and it needs neither a signal nor a PID.

## What external-dns v0.24.0 does

Read from the source at tag `v0.24.0`. The design depends on each of these.

- **Calls.** `GET <url>` once at start-up (negotiation), then every sync
  (default every minute): `GET /records`, `POST /adjustendpoints`, and
  `POST /records` when there are changes. Only `Accept` and `Content-Type`
  headers are sent.
- **Success is exact.** `GET /records` and `POST /adjustendpoints` must
  return 200; `POST /records` must return 204. Negotiation needs a 2xx and
  the exact header
  `Content-Type: application/external.dns.webhook+json;version=1`.
- **Errors.** A response of 500 to 510 is a soft error: logged, retried at
  the next sync. Anything else that is not success, including every 4xx, a
  refused connection and a timeout, is a hard error and external-dns exits
  (the pod restarts). The client timeout is 15 seconds.
- **Negotiation body** is `{"include":["<zone>"]}`. An unknown key such as
  `filters` is silently ignored and leaves a filter that matches everything.
- **Change set** keys are `create`, `updateOld`, `updateNew`, `delete`; empty
  lists are omitted.
- **Endpoints** are `{dnsName, targets[], recordType, setIdentifier?,
  recordTTL?, labels?, providerSpecific?}`. An ingress with IPv4 and IPv6
  addresses yields two endpoints for the name, one A and one AAAA; several
  addresses of one family are several targets on one endpoint. Names arrive
  lowercase with no trailing dot.
- **`/adjustendpoints`** receives only the desired endpoints from sources,
  never TXT records, and its response replaces the desired list. An endpoint
  dropped there is not planned; if an owned record of that name and type
  exists it is deleted.
- **Updates.** The plan updates a record every sync when the desired TTL is
  set and differs from the returned one, or when `providerSpecific` differs
  in either direction. Labels are not compared.
- **TXT registry.** Ownership records are ordinary TXT endpoints named with
  a type prefix (`a-app.home.example.test`, `aaaa-app...`), sent in the same
  `POST /records` as the records they own, each with the label
  `ownedRecord=<name>`. The target is one string that includes literal double
  quotes. external-dns reads them back from `GET /records` and matches on the
  exact name and value; a record with no ownership TXT is treated as someone
  else's and left alone.
- **Flags.** `--policy` has no default and must be set. `--registry`
  defaults to `txt`.

## Design

### Command and process

`dnsmasq-manager serve` takes no flags. It reads its configuration from the
environment, loads the managed file, binds all listeners, then serves until
SIGINT or SIGTERM, when it stops accepting, gives in-flight requests five
seconds and exits 0. Any start-up problem (bad config, unreadable state, a
listener that cannot bind) is returned as an error from `RunE` before anything
is served; `main` prints it and exits 1. If a listener fails while running,
the others are shut down and `serve` returns the error.

Logging is `log/slog` text lines on stdout. Every mutation is logged at info
with who (listener and remote IP), what (name and type), and the values before
and after. The token is never logged.

### Configuration

All variables are prefixed `DNSMASQ_MANAGER_`. Every problem found is reported
in one error, one line each, naming the variable. Unknown `DNSMASQ_MANAGER_*`
variables are an error, to catch typos. The four that belong to `install.sh`
(`VERSION`, `BASE_URL`, `INSTALL_DIR`, `OS_RELEASE`) are ignored rather than
refused, so `serve` runs in a shell that exported them for the installer; a
test reads the names from `install.sh` so the two lists cannot drift.

| Variable | Default | Meaning |
|---|---|---|
| `ZONES` | required | Comma-separated zones records may live in, e.g. `home.example.test`. |
| `ALLOWED_CIDRS` | `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16` | Prefixes record addresses must fall in. AAAA needs a v6 prefix added here. |
| `HOSTS_DIR` | `/etc/dnsmasq.hosts.d` | dnsmasq's `hostsdir`. Must exist and be a directory. |
| `DNS_PROBE_ADDR` | `127.0.0.1:53` | Where `/healthz` asks dnsmasq a question. |
| `API_ADDR` | `127.0.0.1:8443` | REST listener. |
| `API_TOKEN` | | Bearer token. Exactly one of this and `API_TOKEN_FILE`. |
| `API_TOKEN_FILE` | | File holding the token; trailing newline trimmed; refused if readable by group or others. |
| `API_TLS_CERT`, `API_TLS_KEY` | | PEM paths; both or neither. |
| `API_PLAIN_HTTP` | `false` | `true` serves REST without TLS. Exactly one of this and the cert pair. |
| `WEBHOOK_ADDR` | `127.0.0.1:8888` | Webhook listener, or `off`. |
| `WEBHOOK_ALLOWED_SOURCES` | `127.0.0.1/32` | Comma-separated IPs or prefixes allowed to call the webhook. |
| `WEB_ADDR` | `127.0.0.1:80` | Web page listener, or `off`. |
| `ALLOW_WILDCARD_BIND` | `false` | `true` permits `0.0.0.0` or `::` in an address. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |

Rules: addresses are a literal IP and port, never a host name; the three
addresses must differ; the token is at least 32 characters and holds no
whitespace or control character (the presented token is trimmed, so one with
surrounding whitespace could never match); a zone is a valid host name of at
least two labels, under the same label rules as record names, stored
lowercase without a trailing dot. An IPv4-mapped address such as
`[::ffff:127.0.0.1]:8443` is held as the IPv4 address it maps, so the
wildcard and must-differ rules apply to it; an IPv4-mapped IP or prefix in
`ALLOWED_CIDRS` or `WEBHOOK_ALLOWED_SOURCES` is refused, since it could never
match.

Port 80 cannot be bound by an unprivileged process until #6 grants the
capability, and a listener that cannot bind stops start-up. Until then a
non-root run sets `WEB_ADDR` to a high port or `off`; the README says so and
the tests always set it.

### Records and validation

A **record** is a name, a type (`A` or `AAAA`) and one or more addresses. A
**name** has one **source**, `api` or `external-dns`: who created it and who
may change it. A name with no records left is forgotten along with its source.

Validation, applied identically to both APIs and again when the file is
loaded:

- The name is lowercased and one trailing dot removed. It must be at most 253
  characters, each label 1 to 63 of `a-z`, `0-9` and `-`, not starting or
  ending with `-`. So no wildcard and no underscore.
- It must be a strict subdomain of a configured zone: the zone itself is
  refused.
- The type is `A` or `AAAA`. Anything else, including `CNAME`, is unsupported.
- There is at least one address and at most 16; each parses as an IP of the
  type's family (no zone suffix, no IPv4-mapped IPv6) and falls inside an
  allowed prefix. Duplicates are removed and the list is kept sorted.

**TXT ownership records** from external-dns are kept as state and never
published. Each is a name, a list of target strings kept byte for byte, and
the `ownedRecord` label if present, stored lowercase with one trailing dot
removed so it compares equal to the record name it owns. Because they are never published, their
names are checked loosely: lowercase printable ASCII with no spaces, at most
300 characters, ending in `.<zone>` for a configured zone. The label rules for
records do not apply, since external-dns builds these names by adding a prefix
such as `aaaa-` to a record name that may already be at a limit, and one name
the service refused would fail every sync. There is one per name; at most 4
targets of at most 1024 bytes each, and an `ownedRecord` label of at most 253
bytes, the longest record name it could own.

### The managed file

`<HOSTS_DIR>/dnsmasq-manager.hosts`, mode 0644. Example:

```
# Generated by dnsmasq-manager. Do not edit: a file changed by hand is refused at start-up.
#@ format 1
#@ name app.home.example.test external-dns
10.0.0.80 app.home.example.test
fd00:10::80 app.home.example.test
#@ name nas.home.example.test api
10.0.0.40 nas.home.example.test
#@ txt {"dnsName":"a-app.home.example.test","targets":["\"heritage=external-dns,external-dns/owner=default,external-dns/resource=ingress/default/app\""],"ownedRecord":"app.home.example.test"}
```

- Names in byte order; under each `#@ name` line its IPv4 addresses then its
  IPv6 addresses, each sorted, one per line. Then the TXT lines in name order,
  each a single-line JSON object with keys in the order shown. LF endings, a
  final newline, no blank lines. Fields are separated by single spaces, except
  inside a TXT line's JSON strings, which hold targets and owned records byte
  for byte, runs of spaces included.
- dnsmasq sees only the address lines; `#` lines are comments to it.
- **Rendering is canonical:** a state has exactly one rendering.

**Load** (start-up only):

- File missing: empty state. Nothing is written until the first change.
- Otherwise parse strictly, validate every record against the current
  configuration, re-render, and require the result to equal the file byte for
  byte. Any failure refuses to start with `path:line: reason` and leaves the
  file alone. This is how a hand edit, a truncated file, or a record the
  current zones and prefixes no longer allow is caught.
- The way out is in the operator's hands and documented in the README: put the
  configuration back, or remove the named name's block (its `#@ name` line and
  address lines) or TXT line. A file with whole entries removed is still
  canonical, so it loads. There is no repair command.
- Leftover `.dnsmasq-manager.hosts.*` temporary files from a crash are
  removed. No other file in the directory is touched, ever.

**Save:** write a temporary file `.dnsmasq-manager.hosts.<random>` in the same
directory, sync it, rename it over the managed file, sync the directory.
Once the rename succeeds the write counts as done: a failed directory sync
after it only weakens durability across a power loss and is not reported.
dnsmasq ignores dotfiles, so it never reads a partial file. A state with no
records still writes the two header lines rather than deleting the file. No
SIGHUP is sent and dnsmasq's PID is not needed.

### The store

One in-memory state guarded by a mutex; reads return copies. Every write is:
build the new state from the current one, validate it, save it, and only then
replace the in-memory state and log. If any step fails the state and the file
are unchanged. A write that changes nothing does not touch the file. Both APIs
write through the same store, so writes are serialised.

A change made to the file by hand while the service runs is not noticed and is
overwritten by the next write.

### REST API

Listener `API_ADDR`; TLS (minimum 1.2) unless `API_PLAIN_HTTP=true`. JSON in
and out. Every route except `/healthz` needs `Authorization: Bearer <token>`,
compared in constant time; otherwise 401 with `WWW-Authenticate: Bearer`.

| Route | Result |
|---|---|
| `GET /healthz` | 200 or 503, see [Health](#health). |
| `GET /v1/records` | 200 `{"records":[{"name","type","values":[...],"source"}]}`, sorted by name then type. TXT ownership records are not listed. |
| `PUT /v1/records/{name}` | Body `{"type":"A","values":["10.0.0.80"],"ttl":300}`. Sets that name's record of that type; the other type is untouched. 200 with the record. Idempotent. `ttl` is optional, must be a non-negative integer and is ignored. |
| `DELETE /v1/records/{name}` | Removes every record of the name, or only one type with `?type=A`. 204 whether or not it existed. |

Errors are `{"error":"<message>"}`:

- 400 malformed JSON, unknown field, or a bad or unknown query parameter.
  Each route accepts only its own parameters (`DELETE` takes `force` and
  `type`, `PUT` takes `force`, `GET /v1/records` and `/healthz` take none);
  any other, matched case-sensitively, is refused by name, so a typo such as
  `?Type=AAAA` on `DELETE` cannot remove every type of the name.
- 401 missing or wrong token.
- 409 the name's source is `external-dns` and `?force=true` was not given.
- 413 body over 64 KiB.
- 422 validation failure, including an unsupported type such as `CNAME`.
- 500 the file could not be written.

**`?force=true` takes a name over from external-dns:** the change is applied,
the name's source becomes `api`, and the TXT ownership records whose
`ownedRecord` is that name are dropped. external-dns then sees a record it
does not own and leaves it alone. On `DELETE` the records and those TXT
records go together.

Any successful `PUT` or `DELETE` of a name, forced or not, drops the TXT
ownership records whose `ownedRecord` is that name, so a TXT external-dns
created on its own can never make it believe it owns a name the API holds.

### Webhook API

Listener `WEBHOOK_ADDR`, plain HTTP. A request whose source IP (the
connection's remote address; forwarding headers are ignored) is not in
`WEBHOOK_ALLOWED_SOURCES` gets 403. Every response carries
`Content-Type: application/external.dns.webhook+json;version=1`. Both `POST` routes
require that same media type as the request's `Content-Type` (compared as a
parsed media type, so `; version=1` with a space is accepted) and answer
anything else with 415 before reading the body. Request
bodies are capped at 1 MiB and must be exactly one JSON value; anything but
whitespace after it, even a stray `]` or `}`, makes the body malformed.

| Route | Result |
|---|---|
| `GET /` | 200 `{"include":[<zones>]}`. |
| `GET /records` | 200, a JSON array: every A and AAAA record of either source as `{dnsName, targets, recordType}`, then every TXT ownership record as `{dnsName, targets, recordType:"TXT"}` with its targets exactly as received. No `recordTTL`, `labels` or `providerSpecific`. An empty state is `[]`. |
| `POST /adjustendpoints` | 200, the endpoints the service would accept, see below. |
| `POST /records` | 204 once the whole change set is applied, else 500 and nothing is written. |

**`/adjustendpoints`** keeps an endpoint only if it is `A` or `AAAA`, has no
`setIdentifier`, and its name and every target pass validation. Kept endpoints
are returned with `recordTTL` and `providerSpecific` removed and with their
name and targets in exactly the form `GET /records` would return them
(lowercase name, addresses in canonical text form, deduplicated and sorted),
so external-dns does not plan an update every sync. Labels pass through.
Dropped
endpoints (a CNAME, a name outside the zones, an address outside the allowed
prefixes) are reported in the log and the history, but only when the set of
dropped endpoints differs from the same caller's previous call, so a standing
mismatch does not write a line a minute. The previous set is kept per caller
IP, for at most 64 callers with the least recently seen forgotten first, so
two external-dns instances with different dropped sets do not report on every
sync; the log line names the caller. A malformed body is a 500, never a 4xx.

**`POST /records`** is applied to a copy of the state in this order: `delete`,
`updateOld` (as removals), `updateNew`, `create` (both as "set this name and
type to these targets"). Then the result is validated and saved once.

- A/AAAA: deleting something absent is fine; creating something that exists
  with source `external-dns` replaces it. Names it creates get source
  `external-dns`.
- TXT: set or removed by name; removal ignores the value.
- The whole set fails with 500, and nothing is written, if any entry is
  another record type, has a `setIdentifier`, fails validation, or would
  create, change or delete a record of a name whose source is `api`, or a TXT
  record whose `ownedRecord` is such a name: the incoming entry's label and
  the owner of the TXT already stored under that name are both checked. With the
  TXT registry external-dns never sends the last kind, so it means a
  misconfiguration (for example `--registry=noop`) and failing loudly is
  right. The response body and the log say which entry and why.
- An empty change set is a 204 with no write.

Never a 4xx from these four routes other than the allowlist's 403, 404/405
for unknown routes and methods, and 415 for a `POST` without the webhook
media type. external-dns v0.24.0 always sends that `Content-Type` on both
`POST`s, so a request without it is not external-dns and the 415 cannot end
a sync; refusing it stops a browser on an allowlisted address from sending a
change set cross-origin as `text/plain`, which needs no preflight.
Negotiation matches the path `/` exactly (`GET /{$}` in `net/http`), so an
unknown path is a 404 and not a negotiation reply, and the 403, 404, 405 and
415 replies carry the webhook `Content-Type` too.

### Health

`GET /healthz` on the REST listener, no token:

- **hosts_dir:** the directory exists and the process may create files in it.
  The check leaves nothing behind.
- **dnsmasq:** a UDP query for `version.bind` (class CH, type TXT) to
  `DNS_PROBE_ADDR` gets a well-formed reply with the matching ID within one
  second. Any response code counts: dnsmasq answers this locally, so it does
  not depend on upstream servers. The query is built by hand; no DNS library.

200 `{"status":"ok","checks":{"hosts_dir":"ok","dnsmasq":"ok"}}`, or 503 with
`"status":"unhealthy"` and the failing check's value replaced by its error.

`/healthz` and the web page share one result: concurrent requests wait on one
run of the checks, and its result answers every request for 2 seconds after
it finishes, so unauthenticated requests cannot drive the directory check and
the probe at will. A request that gives up does not cut the run short for the
others.

### History

A ring of the last 50 entries in memory, lost on restart: time, listener,
method, path (no query values beyond `force` and `type`), status, remote IP,
duration, and a one-line summary such as `set app.home.example.test A` or
`create 2, update 0, delete 1`. Bodies and headers are never kept.

- REST: every request except `/healthz`, including refused ones.
- Webhook: every `POST /records`; other calls only when the status is 400 or
  above; and the dropped-endpoint reports from `/adjustendpoints`.

### Web page

Listener `WEB_ADDR`, plain HTTP, no JavaScript, `html/template` with the
template and stylesheet embedded in the binary. Two routes: `GET /` (exactly)
for the page and `GET /style.css`. The page shows:

1. Health: the two checks, as on `/healthz`.
2. Records: name, type, addresses, source. A count of TXT ownership records.
3. The last 50 history entries, newest first.

It refreshes itself every 30 seconds with a `<meta>` tag and sends
`Content-Security-Policy: default-src 'none'; style-src 'self'`. Nothing on it
changes state.

### Code layout

Packages under `internal/`, each tested on its own:

| Package | Job |
|---|---|
| `records` | Record and TXT types, the validation policy, the store |
| `hostsfile` | Render and parse the managed file; atomic load and save |
| `config` | Environment into a checked `Config` |
| `health` | Hosts-dir check and dnsmasq probe, and the cache that shares their result |
| `history` | The ring and the recording middleware |
| `api` | REST handlers and bearer auth |
| `webhook` | external-dns handlers and the source allowlist |
| `web` | The page |
| `server` | Listeners, TLS, graceful shutdown |
| `cli` | `serve.go` with `newServeCommand()`, following `version.go` |

`main.go` gains a signal-cancelled context passed to the command.

### Testing

- Unit tests, failing test first, for: validation; render and parse round
  trip including refusal of non-canonical files; save atomicity; store
  rollback on a failed save; config errors reported together; change-set
  logic and its all-or-nothing rule; ownership and `force`; auth; the
  allowlist; history selection; the page rendering.
- Integration (`make integration-docker`, dnsmasq 2.91 on Alpine 3.23): the
  real binary beside a real dnsmasq. Through REST and through a webhook
  change set: add, update and delete are seen by `dig`, for A and AAAA; a
  file with `#@` lines is served correctly; state survives a restart of the
  service; `/healthz` turns 503 when dnsmasq is stopped. The Dockerfile and
  Makefile build the binary for these tests.
- End to end (`make e2e`, containers only, also a GitHub workflow job): the
  real external-dns v0.24.0 image, the service beside dnsmasq 2.91, and a
  throwaway single-node Kubernetes API for external-dns to read Ingresses
  from. No ingress controller runs; the harness sets each Ingress's status
  address itself. external-dns runs with the flags from the values snippet,
  pointed at the service's webhook, with a short `--interval`. It checks:
  - an Ingress in the zone becomes an A record that `dig` sees, with its TXT
    ownership records held in the managed file;
  - after several more syncs the managed file has not been rewritten and
    external-dns has logged no update (no update loop);
  - changing the Ingress's address updates the record, and deleting the
    Ingress removes the record and its TXT records;
  - an Ingress external-dns cannot be served (a host outside the zone, an
    address outside the allowed prefixes) leaves the others syncing and
    external-dns running;
  - a name created over REST is left alone when an Ingress asks for it;
  - after a REST `force` take-over, external-dns leaves the name alone and
    keeps syncing the rest.
- The web page is checked in a real browser, with screenshots on the pull
  request.

### Documentation delivered

README (run, variables, the `hostsdir=` line dnsmasq needs, token rotation by
restart, rollback by deleting the managed file), CLAUDE.md, an example conf.d
environment file, an external-dns values snippet (`--provider=webhook`,
`--webhook-provider-url`, `--source=ingress`,
`--ingress-class=traefik-private`, `--domain-filter=<private-zone>`,
`--registry=txt`, `--txt-owner-id`, `--policy=sync`), and the list of
requirements that proved wrong.

## Calls to check

Proposals the owner has not yet seen in this form:

1. **REST uses `values` (a list), not `value`.** external-dns can send several
   addresses for one name, so the store must hold a list; one field name for
   both cases keeps the API honest.
2. **`PUT` sets one type; `DELETE` removes the whole name** unless `?type=` is
   given.
3. **Source is per name, not per record.** A name is either the API's or
   external-dns's, both A and AAAA.
4. **`force` takes the name over** and drops its ownership TXT records, rather
   than overriding it until external-dns next syncs.
5. **`/adjustendpoints` drops what validation would refuse**, not only CNAMEs,
   so one bad Ingress cannot fail every sync for all the others.
6. **The webhook returns API-created records in `GET /records`** so
   external-dns sees the name is taken and does not try to create it.
7. **A webhook change to an `api` name fails the whole set with 500.**
8. **Start-up refuses a file whose records the current zones or prefixes no
   longer allow**, rather than loading or dropping them. Recovery is by
   restoring the configuration or removing the named entry by hand; there is
   no repair command.
9. **`WEBHOOK_ADDR=off` and `WEB_ADDR=off`** disable those listeners.
10. **Token at least 32 characters; token file refused if group- or
    world-readable; unknown `DNSMASQ_MANAGER_*` variables are an error.**
11. **Text log lines**, not JSON.
12. **No SIGHUP, no CNAME, TTL ignored**, as in the plan.
13. **TXT ownership names are checked loosely** (inside a zone, bounded
    length), so a long record name cannot produce a TXT name that fails every
    sync.
14. **A real external-dns v0.24.0 in an end-to-end harness** (owner's
    decision at approval, replacing the draft's "no live external-dns"). See
    [Testing](#testing).

## Not verified

- Until the end-to-end harness runs, the external-dns behaviour above is
  read from its source only. In particular the take-over in call 4
  (external-dns leaving alone a record whose TXT has gone) is inferred from
  the plan and registry code. If the harness shows otherwise, the design is
  corrected before the code is.
- A real ingress controller publishing the status address; the harness sets
  it by hand.
- That dnsmasq 2.91 serves AAAA lines and ignores `#@` lines in a
  hosts-directory file. The integration tests in this change check both.
- The production dnsmasq build's compile-time options since the upgrade.

## Left to the builder

- Exact Go types, function names and error wording.
- How the hosts-directory writability check is done without leaving a file.
- The page's look, within: no JavaScript, embedded assets, readable on a
  phone.
- HTTP server timeouts, provided a slow client cannot hold a connection open
  indefinitely and the webhook answers well inside external-dns's 15 seconds.
- How integration tests start and stop the binary, reusing the existing
  harness (`startDnsmasq`, `eventually`, `freePort`, `query`).
- How the end-to-end harness provides the Kubernetes API (for example k3s in
  a container under Docker Compose, or kind), provided `make e2e` needs only
  Docker and runs the same way locally and in the workflow.
