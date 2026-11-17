# Runs the integration probes against the dnsmasq version the production
# host runs. Build from the repo root: `make integration-docker`.

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go test -c -tags integration -o /out/integration.test ./test/integration/

# Alpine 3.12 matches the production host and packages dnsmasq 2.85.
FROM alpine:3.12
RUN apk add --no-cache dnsmasq bind-tools
# Fail the build rather than probe a dnsmasq the production host does not run.
RUN dnsmasq --version | grep -q 'version 2.85'
COPY --from=build /out/integration.test /src/test/integration/
COPY testdata/ /src/testdata/
WORKDIR /src/test/integration
CMD ["./integration.test", "-test.v"]
