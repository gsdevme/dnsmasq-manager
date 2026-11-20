# Runs the integration probes, and the service binary beside dnsmasq, against
# the dnsmasq version the production host runs. Build from the repo root:
# `make integration-docker`.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go test -c -tags integration -o /out/integration.test ./test/integration/
RUN CGO_ENABLED=0 go build -trimpath -o /out/dnsmasq-manager ./cmd/dnsmasq-manager

# Alpine 3.23 is the release production runs and packages dnsmasq 2.91;
# both are the minimums the service supports.
FROM alpine:3.23
RUN apk add --no-cache dnsmasq bind-tools
# Fail the build rather than probe a dnsmasq the production host does not run.
RUN dnsmasq --version | grep -q 'version 2.91'
COPY --from=build /out/integration.test /src/test/integration/
COPY --from=build /out/dnsmasq-manager /usr/local/bin/dnsmasq-manager
# The serve tests run this binary rather than building one, as the final
# image has no Go toolchain.
ENV DNSMASQ_MANAGER_BIN=/usr/local/bin/dnsmasq-manager
COPY testdata/ /src/testdata/
WORKDIR /src/test/integration
CMD ["./integration.test", "-test.v"]
