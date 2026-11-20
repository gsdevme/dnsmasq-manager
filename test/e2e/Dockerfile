# The `dns` service of the end-to-end harness: dnsmasq 2.91 on Alpine 3.23
# with `dnsmasq-manager serve` beside it, writing dnsmasq's hosts directory.
# Build from the repo root: `make e2e` (through test/e2e/compose.yaml).

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/dnsmasq-manager ./cmd/dnsmasq-manager

# Alpine 3.23 is the release production runs and packages dnsmasq 2.91; both
# are the minimums the service supports. bind-tools gives dig and curl lets
# the harness call the REST API from inside the container.
FROM alpine:3.23
RUN apk add --no-cache dnsmasq bind-tools curl
# Fail the build rather than test against a dnsmasq production does not run.
RUN dnsmasq --version | grep -q 'version 2.91'
COPY --from=build /out/dnsmasq-manager /usr/local/bin/dnsmasq-manager
COPY testdata/dnsmasq/dnsmasq.conf /etc/dnsmasq.conf
COPY test/e2e/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN mkdir -p /etc/dnsmasq.hosts.d
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
