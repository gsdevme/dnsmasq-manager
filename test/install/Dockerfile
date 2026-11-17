# Image for the end-to-end installer test, run by `make test-install-docker`.
#
# Alpine 3.23 is the minimum the installer supports, and its /bin/sh, wget
# and sha256sum are the BusyBox applets the installer meets in production.
# busybox-extras provides httpd to serve the release files locally; bash
# runs the stub suite. The release files themselves are not copied in: dist/
# is excluded from the build context and bind-mounted at /dist instead.
FROM alpine:3.23
RUN apk add --no-cache busybox-extras bash
COPY install.sh /src/install.sh
COPY test/install/ /src/test/install/
CMD ["sh", "/src/test/install/e2e.sh"]
