# What the requirements got wrong

Deliverable 4 of the [original requirements](requirements/dnsmasq-api.md#original-requirements):
each statement in them that turned out to be wrong or unsupported when
checked against dnsmasq 2.91 on Alpine 3.23 and external-dns v0.24.0, and
what is true instead. The evidence is in
[`dnsmasq-findings.md`](dnsmasq-findings.md) (dnsmasq probes), the
[serve design](superpowers/specs/2026-11-19-serve-design.md#what-external-dns-v0240-does)
(external-dns read from its source at `v0.24.0`), and the integration and
end-to-end tests (`make integration-docker`, `make e2e`).

## dnsmasq and the DNS host

- **"Deletes must write the file and then send SIGHUP."** Not on 2.91.
  Since dnsmasq 2.88 the hosts directory handles removals through inotify
  like any other change: an added, changed or removed line, and deleting
  the whole file, are all served as soon as dnsmasq reads the directory. The
  service sends no signal, and so needs neither dnsmasq's PID nor a sudoers
  rule or reload hook to signal it. The "narrowest way to signal dnsmasq"
  in the security section is not needed.
- **systemd: "systemd unit included", "`systemctl reload dnsmasq`",
  "stdout/journald".** The host runs Alpine, which uses OpenRC. The unit
  becomes an OpenRC init script and conf.d file (issue #6), there is no
  reload step, and with no journald the logs are plain lines on stdout.
- **Rollback: "delete the managed file + reload".** No reload: stop the
  service and delete `dnsmasq-manager.hosts`; dnsmasq 2.91 drops its records
  at once (checked by hand, not by a probe).
- **CNAME records in the REST API.** Unsupported in v1. A hosts file cannot
  hold a CNAME, and a conf-dir `cname=` file lies outside the hosts
  directory, where a change needs a dnsmasq restart or reload that was not
  probed and permission to signal dnsmasq. Nothing in the requirements
  needs CNAME. The REST API refuses it with 422; the webhook drops CNAME
  endpoints in `/adjustendpoints` so they never fail a sync.
- **`ttl` on records.** A hosts file has no per-record TTL. The REST API
  accepts a non-negative `ttl` and ignores it; the webhook strips
  `recordTTL`.
- **`GET /v1/records` returns `{name, type, value, ttl?}`.** external-dns can
  send several addresses for one name and type, so a record holds a list:
  `{name, type, values[], source}`. `source` says whether the API or
  external-dns owns the name.

## external-dns v0.24.0

- **Negotiation "returns the domain filter".** The body must be
  `{"include":["<zone>", ...]}`. An unknown key, such as `filters`, is
  silently ignored and leaves a filter that matches every name. The reply
  also needs the exact header
  `Content-Type: application/external.dns.webhook+json;version=1`.
- **`POST /records` body `{Create[], UpdateOld[], UpdateNew[], Delete[]}`.**
  The keys are lowercase camelCase: `create`, `updateOld`, `updateNew`,
  `delete`. Empty lists are omitted.
- **"Apply and return 204."** Success codes are exact: `POST /records` must
  return 204, and `GET /records` and `POST /adjustendpoints` must return
  200. Anything else is an error.
- **Errors were not covered.** Only a 500 to 510 reply is a soft error that
  external-dns retries at the next sync. Every 4xx, a refused connection
  and a timeout (15 seconds) are hard errors that make external-dns exit
  and its pod restart. So the webhook answers every failure inside a route
  with 500; its only 4xx replies are 403 from the allowlist and 404 or 405
  for unknown routes.
- **`/adjustendpoints` "returns the endpoints unchanged (or normalised)".**
  Unchanged is wrong twice over. An endpoint the service would refuse must
  be dropped there, or one bad Ingress fails every sync for all the others.
  And the kept endpoints must come back exactly as `GET /records` will
  return them (lowercase name, canonical sorted addresses, no `recordTTL` or
  `providerSpecific`), or external-dns plans an update every sync.
- **Bearer-token auth on every endpoint.** Impossible on the webhook side:
  the v0.24.0 webhook client sends only `Accept` and `Content-Type`, and has
  no flags for custom headers, client certificates or a custom CA. So mTLS
  through a reverse proxy is not possible either. The webhook has its own
  plain-HTTP listener guarded by a source-IP allowlist of the cluster's
  addresses; the bearer token stays on the REST API.
- **"Registry and policy flags matching your ownership decision."**
  `--policy` has no default in v0.24.0 and must be set (`--policy=sync`).
  `--registry` defaults to `txt`.
- **TXT ownership: "store TXT ownership records in the service's own
  state".** Confirmed workable, and chosen over `--registry=noop`.
  external-dns sends ownership records as ordinary TXT endpoints in the
  same `POST /records` as the records they own and reads them back from
  `GET /records`, matching on the exact name and value. The service keeps
  them in its managed file as `#@ txt` lines dnsmasq ignores, and never
  publishes them in DNS.
- **A remote webhook through the Helm chart.** The requirements assume the
  values snippet alone configures external-dns. The external-dns chart
  1.24.0 (the one for v0.24.0) always adds a webhook sidecar container when
  the provider is `webhook`, refuses to render without an image for it, and
  has no value to turn it off. A remote webhook needs a placeholder image
  and a post-render patch that removes the sidecar; see
  [`examples/external-dns-values.yaml`](examples/external-dns-values.yaml).
  The webhook URL and the ingress class have no chart keys and go through
  `extraArgs`.
