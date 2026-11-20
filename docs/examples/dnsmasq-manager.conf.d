# Example environment for `dnsmasq-manager serve`, in the style of an OpenRC
# /etc/conf.d file (sourced by sh).
#
# This is an example only. Nothing installs it yet: the OpenRC init script,
# the service user and the installed /etc/conf.d/dnsmasq-manager arrive with
# issue #6, and that change decides how the file is read. Until then, source it
# by hand before running the binary:
#
#   ( . ./dnsmasq-manager.conf.d && exec dnsmasq-manager serve )
#
# Every value below is a placeholder. Lines left commented show the default.
# An empty value counts as unset, so it means the default too, not "off".
# Any other DNSMASQ_MANAGER_* variable is refused at start-up, to catch typos.
# Addresses are always a literal IP and port, never a host name.

# --- Records -----------------------------------------------------------------

# Required. Comma-separated zones records may live in. A record must be a
# strict subdomain of one of them; the zone itself is refused.
export DNSMASQ_MANAGER_ZONES=home.example.test

# Comma-separated prefixes every record address must fall inside. AAAA records
# need an IPv6 prefix here, for example ...,fd00:10::/64.
#export DNSMASQ_MANAGER_ALLOWED_CIDRS=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16

# dnsmasq's hostsdir: it must exist, be a directory, and be the directory named
# by `hostsdir=` in the dnsmasq config. The service writes only
# dnsmasq-manager.hosts and its own .dnsmasq-manager.hosts.* temporary files.
#export DNSMASQ_MANAGER_HOSTS_DIR=/etc/dnsmasq.hosts.d

# Where GET /healthz sends its version.bind query to check dnsmasq answers.
#export DNSMASQ_MANAGER_DNS_PROBE_ADDR=127.0.0.1:53

# --- REST API ----------------------------------------------------------------

# REST listener. Bearer token on every route except /healthz.
export DNSMASQ_MANAGER_API_ADDR=10.0.0.53:8443

# The bearer token, at least 32 characters. Set exactly one of these two.
# The file is preferred: it is read once at start-up, trailing line endings are
# trimmed, and it is refused if group or others can read it (use mode 0600).
# Changing the token takes a restart.
export DNSMASQ_MANAGER_API_TOKEN_FILE=/etc/dnsmasq-manager/api-token
#export DNSMASQ_MANAGER_API_TOKEN=

# TLS for the REST listener: PEM certificate and key, both or neither.
export DNSMASQ_MANAGER_API_TLS_CERT=/etc/dnsmasq-manager/api.crt
export DNSMASQ_MANAGER_API_TLS_KEY=/etc/dnsmasq-manager/api.key

# true serves REST without TLS. Set this or the certificate pair, not both.
#export DNSMASQ_MANAGER_API_PLAIN_HTTP=false

# --- external-dns webhook ----------------------------------------------------

# Webhook listener, or off. Plain HTTP: external-dns v0.24.0 cannot send an
# auth header, so the allowlist below is its only guard. Put it on the LAN
# address the cluster reaches (--webhook-provider-url in the external-dns
# values).
export DNSMASQ_MANAGER_WEBHOOK_ADDR=10.0.0.53:8888

# Comma-separated IPs or prefixes allowed to call the webhook; anything else
# gets 403. The connection's own address is checked; forwarding headers are
# ignored. Usually the cluster nodes' LAN addresses.
export DNSMASQ_MANAGER_WEBHOOK_ALLOWED_SOURCES=10.0.0.21,10.0.0.22

# --- Web page ----------------------------------------------------------------

# Read-only status page, or off. Port 80 needs root until #6 grants the
# capability to bind it; a non-root run sets a high port or off.
export DNSMASQ_MANAGER_WEB_ADDR=10.0.0.53:80

# --- Process -----------------------------------------------------------------

# true allows 0.0.0.0 or :: in a listener address.
#export DNSMASQ_MANAGER_ALLOW_WILDCARD_BIND=false

# debug, info, warn or error.
#export DNSMASQ_MANAGER_LOG_LEVEL=info
