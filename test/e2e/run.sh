#!/usr/bin/env bash
# End-to-end test of `dnsmasq-manager serve` against the real external-dns
# v0.24.0, run by `make e2e`. Needs only Docker (with the compose plugin).
#
# Brings up test/e2e/compose.yaml (k3s, the service beside dnsmasq 2.91, and
# external-dns), then runs eight checks in order, printing `ok` or `FAIL` for
# each. The first failure prints every container's log and the managed file
# and exits 1. The stack is always torn down, volumes included.
#
# Everything is driven from inside the containers: kubectl from k3s, dig and
# curl from the dns container. Nothing listens on the host.
#
# Waits for a state poll once a second until a timeout (E2E_TIMEOUT seconds).
# The only fixed waits are the "nothing must happen" windows, which last
# four external-dns sync intervals (E2E_SYNC_INTERVAL seconds each; the same
# variable sets external-dns's --interval in compose.yaml).
#
# Under pipefail a `producer | grep -q` condition is unreliable: grep exits at
# the first match, the producer may then die of SIGPIPE, and a match reads as
# no match. So every condition captures the producer's output in a variable,
# fails or returns non-zero when the producer itself fails, and only then
# greps a here-string.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)

export E2E_SYNC_INTERVAL="${E2E_SYNC_INTERVAL:-5}"
timeout="${E2E_TIMEOUT:-90}"
window=$((4 * E2E_SYNC_INTERVAL))

# Must match DNSMASQ_MANAGER_API_TOKEN in compose.yaml. A placeholder.
token=e2e-placeholder-token-not-a-secret-0000
managed=/etc/dnsmasq.hosts.d/dnsmasq-manager.hosts
started=$(date +%s)

compose() { docker compose -f "$here/compose.yaml" "$@"; }
kubectl() { compose exec -T k3s kubectl --kubeconfig=/kube/k3s.yaml "$@"; }
in_dns() { compose exec -T dns "$@"; }

teardown() { compose down --volumes --remove-orphans --timeout 5 >/dev/null 2>&1 || true; }
trap teardown EXIT
trap 'exit 130' INT TERM

dump() {
	local s
	for s in k3s dns external-dns; do
		echo "===== logs: $s ====="
		compose logs --no-color --no-log-prefix "$s" 2>&1 | tail -n 400 || true
	done
	echo "===== $managed ====="
	managed_file || echo "(cannot read $managed)"
}

ok() { echo "ok   $*"; }
fail() {
	echo "FAIL $*"
	dump
	exit 1
}

# eventually DESC CMD...: runs CMD once a second until it succeeds, failing
# the run with DESC after $timeout seconds.
eventually() {
	local desc=$1 deadline=$(($(date +%s) + timeout))
	shift
	until "$@" >/dev/null 2>&1; do
		if [ "$(date +%s)" -ge "$deadline" ]; then
			fail "$desc (timed out after ${timeout}s)"
		fi
		sleep 1
	done
}

# hold DESC: waits the four-interval window in which nothing must change.
hold() {
	echo "     waiting ${window}s (4 sync intervals): $1"
	sleep "$window"
}

managed_file() { in_dns cat "$managed" 2>/dev/null; }

# file_holds TEXT: 0 when the managed file holds TEXT, 1 when it does not, 2
# (with a line on stderr) when the file cannot be read.
file_holds() {
	local content
	if ! content=$(managed_file); then
		echo "cannot read $managed" >&2
		return 2
	fi
	grep -qF -- "$1" <<<"$content"
}
has() { file_holds "$1"; }
lacks() {
	local rc=0
	file_holds "$1" || rc=$?
	[ "$rc" -eq 1 ]
}

# answers NAME: the A addresses dnsmasq serves for NAME, sorted, one line,
# space separated. Empty when there is no answer or the query times out
# (names outside the mock's local zones are forwarded to TEST-NET servers
# that never answer). Non-zero when dig cannot be run in the dns container.
answers() {
	local out
	# shellcheck disable=SC2016 # $1 is expanded by the container's sh.
	out=$(in_dns sh -c 'dig +short +time=1 +tries=1 @127.0.0.1 "$1" A 2>/dev/null; true' sh "$1") || return 1
	awk '!/^;/ && NF' <<<"$out" | sort | paste -sd' ' -
}
answers_are() {
	local got
	got=$(answers "$1") || return 1
	[ "$got" = "$2" ]
}
answers_other_than() {
	local got
	got=$(answers "$1") || return 1
	[ "$got" != "$2" ]
}

# ingress NAME HOST ADDRESS: creates an Ingress of class traefik-private for
# HOST and sets its load-balancer status to ADDRESS, as a controller would.
ingress() {
	sed -e "s/__NAME__/$1/" -e "s/__HOST__/$2/" "$here/manifests/ingress.yaml" |
		kubectl apply -f - >/dev/null
	set_status "$1" "$3"
}
set_status() {
	kubectl patch ingress "$1" --subresource=status --type=merge \
		-p "{\"status\":{\"loadBalancer\":{\"ingress\":[{\"ip\":\"$2\"}]}}}" >/dev/null
}

# rest_put NAME ADDRESS [QUERY]: PUT an A record over REST and print the
# HTTP status.
rest_put() {
	in_dns curl -sS -o /dev/null -w '%{http_code}' -X PUT \
		-H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
		-d "{\"type\":\"A\",\"values\":[\"$2\"]}" \
		"http://127.0.0.1:8443/v1/records/$1${3:-}"
}

edns_id() { compose ps --all --quiet external-dns; }
edns_state() { docker inspect -f '{{.State.Status}} {{.RestartCount}} {{.State.StartedAt}}' "$(edns_id)"; }

# edns_alive: external-dns is still the process started at bring-up: running,
# never restarted. v0.24.0 exits on any hard webhook error.
edns_alive() { [ "$(edns_state)" = "$edns_first" ]; }

# service_log SERVICE: everything SERVICE has logged so far; non-zero when
# compose cannot read it.
# log_lines SERVICE: how many log lines SERVICE has written so far.
# log_since SERVICE N: the lines after the first N.
service_log() { compose logs --no-color --no-log-prefix "$1" 2>&1; }
log_lines() {
	local log
	log=$(service_log "$1") || return 1
	if [ -z "$log" ]; then
		echo 0
	else
		wc -l <<<"$log" | tr -d ' '
	fi
}
log_since() {
	local log
	log=$(service_log "$1") || return 1
	tail -n "+$(($2 + 1))" <<<"$log"
}

# sync_rounds: reads external-dns log lines and prints "rounds clean dirty".
# external-dns v0.24.0 logs nothing per change for a webhook provider; a sync
# with nothing to do ends in "All records are already up to date", a sync
# that applied changes does not. Each round starts with the multiSource
# debug line, so a round followed by another without the up-to-date line is
# a sync that changed something. A round still open at the end is ignored.
sync_rounds() {
	awk '/multiSource: collecting endpoints/ { if (open) dirty++; open = 1; rounds++ }
		/All records are already up to date/ { if (open) clean++; open = 0 }
		END { print rounds + 0, clean + 0, dirty + 0 }'
}

# stamp: the managed file's inode and modification time to the nanosecond.
stamp() { in_dns stat -c '%i %y' "$managed"; }

echo "e2e: external-dns v0.24.0 against dnsmasq-manager (sync interval ${E2E_SYNC_INTERVAL}s)"
teardown
compose build --quiet dns
compose up --detach --wait dns k3s >/dev/null 2>&1 || fail "starting the dns and k3s containers"

eventually "k3s API ready" kubectl get --raw=/readyz
compose exec -T k3s sh -c \
	'sed "s#https://127.0.0.1:6443#https://k3s:6443#" /kube/k3s.yaml > /kube/external-dns.yaml'
kubectl apply -f - <"$here/manifests/ingressclass.yaml" >/dev/null

compose up --detach external-dns >/dev/null 2>&1 || fail "starting external-dns"
edns_first=$(edns_state) || fail "inspecting the external-dns container"
case "$edns_first" in running\ 0\ *) ;; *) fail "external-dns not running: $edns_first" ;; esac
first_sync() {
	local log
	log=$(service_log external-dns) || return 1
	grep -q 'All records are already up to date' <<<"$log"
}
eventually "external-dns completes its first sync" first_sync
echo "     stack up after $(($(date +%s) - started))s"

# 1. An Ingress in the zone becomes an A record dnsmasq serves, with its
#    ownership TXT record held in the managed file.
ingress app app.home.example.test 10.0.0.80
eventually "1: dig app.home.example.test answers 10.0.0.80" answers_are app.home.example.test 10.0.0.80
eventually "1: managed file has the external-dns name block" has '#@ name app.home.example.test external-dns'
eventually "1: managed file has the a-app TXT line" has '#@ txt {"dnsName":"a-app.home.example.test"'
ok "1: Ingress app.home.example.test -> 10.0.0.80 served, a-app TXT held"

# 2. No update loop: four syncs later the file has not been rewritten, the
#    service logged no change and external-dns applied none.
before=$(stamp) || fail "2: cannot stat $managed"
dns_mark=$(log_lines dns) || fail "2: cannot read the dns log"
edns_mark=$(log_lines external-dns) || fail "2: cannot read the external-dns log"
hold "no update loop"
after=$(stamp) || fail "2: cannot stat $managed"
[ "$before" = "$after" ] || fail "2: managed file rewritten: '$before' -> '$after'"
dns_new=$(log_since dns "$dns_mark") || fail "2: cannot read the dns log"
if grep -qE 'record changed|source changed|ownership changed' <<<"$dns_new"; then
	fail "2: service logged a change with nothing to change"
fi
edns_new=$(log_since external-dns "$edns_mark") || fail "2: cannot read the external-dns log"
read -r rounds clean dirty <<<"$(sync_rounds <<<"$edns_new")"
[ "$dirty" -eq 0 ] || fail "2: external-dns planned changes in $dirty of $rounds syncs"
[ "$clean" -ge 2 ] || fail "2: only $clean up-to-date syncs logged in ${window}s"
ok "2: no update loop ($clean syncs all up to date, file untouched: $after)"

# 3. A new status address replaces the old one.
set_status app 10.0.0.81
eventually "3: dig app.home.example.test answers only 10.0.0.81" answers_are app.home.example.test 10.0.0.81
ok "3: status 10.0.0.81 replaces 10.0.0.80"

# 4. Ingresses the service cannot serve leave external-dns running and the
#    others syncing.
ingress other app.other.test 10.0.0.83
ingress bad bad.home.example.test 8.8.8.8
hold "unservable Ingresses stay unserved"
lacks app.other.test || fail "4: app.other.test is in the managed file"
lacks bad.home.example.test || fail "4: bad.home.example.test is in the managed file"
answers_other_than app.other.test 10.0.0.83 || fail "4: app.other.test is served, or dig failed"
bad=$(answers bad.home.example.test) || fail "4: dig bad.home.example.test failed"
case " $bad " in *" 8.8.8.8 "*) fail "4: bad.home.example.test is served 8.8.8.8" ;; esac
edns_alive || fail "4: external-dns exited or restarted: $(edns_state)"
ingress valid valid.home.example.test 10.0.0.82
eventually "4: dig valid.home.example.test answers 10.0.0.82" answers_are valid.home.example.test 10.0.0.82
ok "4: out-of-zone and out-of-prefix Ingresses ignored, external-dns running, valid one served"

# 5. A name created over REST is left alone when an Ingress asks for it.
code=$(rest_put manual.home.example.test 10.0.0.50)
[ "$code" = 200 ] || fail "5: REST PUT manual.home.example.test returned $code"
eventually "5: dig manual.home.example.test answers 10.0.0.50" answers_are manual.home.example.test 10.0.0.50
ingress manual manual.home.example.test 10.0.0.51
hold "REST name not taken by the Ingress"
answers_are manual.home.example.test 10.0.0.50 ||
	fail "5: manual.home.example.test answers '$(answers manual.home.example.test)', not 10.0.0.50"
has '#@ name manual.home.example.test api' || fail "5: manual.home.example.test is no longer an api name"
lacks a-manual.home.example.test || fail "5: external-dns wrote a TXT record for manual.home.example.test"
edns_alive || fail "5: external-dns exited or restarted: $(edns_state)"
ok "5: REST name manual.home.example.test kept at 10.0.0.50, external-dns running"

# 6. A forced REST take-over: external-dns leaves the name alone and keeps
#    syncing the rest.
code=$(rest_put app.home.example.test 10.0.0.99 '?force=true')
[ "$code" = 200 ] || fail "6: forced REST PUT app.home.example.test returned $code"
eventually "6: dig app.home.example.test answers 10.0.0.99" answers_are app.home.example.test 10.0.0.99
lacks a-app.home.example.test || fail "6: the a-app TXT line survived the take-over"
hold "taken-over name left alone"
answers_are app.home.example.test 10.0.0.99 ||
	fail "6: app.home.example.test answers '$(answers app.home.example.test)', not 10.0.0.99"
lacks a-app.home.example.test || fail "6: external-dns wrote the a-app TXT line back"
has '#@ name app.home.example.test api' || fail "6: app.home.example.test is no longer an api name"
edns_alive || fail "6: external-dns exited or restarted: $(edns_state)"
ingress new new.home.example.test 10.0.0.84
eventually "6: dig new.home.example.test answers 10.0.0.84" answers_are new.home.example.test 10.0.0.84
ok "6: forced take-over of app.home.example.test holds at 10.0.0.99, a-app TXT stays gone, new Ingress served"

# 7. Deleting an Ingress removes its record and its TXT record. The name then
#    falls back to the mock config's .home.example.test wildcard.
kubectl delete ingress valid >/dev/null
eventually "7: valid.home.example.test gone from the managed file" lacks valid.home.example.test
eventually "7: valid.home.example.test still answers 10.0.0.82" answers_other_than valid.home.example.test 10.0.0.82
ok "7: deleted Ingress valid.home.example.test removed with its TXT record"

# 8. The webhook refuses callers outside its allowlist. k3s has BusyBox
#    wget, not curl; -S prints the status line.
reply=$(compose exec -T k3s wget -q -S -O /dev/null http://172.30.0.20:8888/ 2>&1 || true)
grep -q 'HTTP/1.1 403' <<<"$reply" || fail "8: webhook call from k3s was not refused with 403: $reply"
edns_alive || fail "8: external-dns exited or restarted: $(edns_state)"
ok "8: webhook call from k3s (172.30.0.10) refused with 403"

echo "e2e: all checks passed in $(($(date +%s) - started))s"
