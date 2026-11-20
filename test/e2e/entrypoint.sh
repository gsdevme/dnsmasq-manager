#!/bin/sh
# Entrypoint of the e2e `dns` container: dnsmasq in the background, then the
# service in the foreground, so the container exits (and the harness sees
# it) if the service stops.
#
# dnsmasq listens on loopback only; the harness queries it with dig from
# inside this container, which is also where /healthz probes it. It logs to
# stderr so its lines appear in `docker compose logs dns`.
set -eu

dnsmasq \
	--keep-in-foreground \
	--conf-file=/etc/dnsmasq.conf \
	--hostsdir=/etc/dnsmasq.hosts.d \
	--listen-address=127.0.0.1 \
	--bind-interfaces \
	--no-hosts \
	--pid-file= \
	--user=root \
	--log-facility=- &

exec /usr/local/bin/dnsmasq-manager serve
