# dnsmasq behaviour findings

What the integration probes in `test/integration/` observed when a real
dnsmasq serves the mock config in `testdata/dnsmasq/dnsmasq.conf` and a
hosts directory (`--hostsdir`) that the tests write to. The service design
relies on these results, so re-run the probes (`make integration-docker`)
before changing how the service writes records or signals dnsmasq.

## Environment

- Production host: Alpine 3.12, dnsmasq 2.85.
- Probe image: `alpine:3.12`, whose `dnsmasq` package is `2.85-r0`, so no
  newer Alpine release was needed. The image build fails on any other version.
- dnsmasq is built with `inotify`, which is what reads hosts-directory
  changes without a signal.
- Each test starts its own dnsmasq with an empty hosts directory and writes a
  single file, `managed.hosts`, by writing a dot-prefixed temporary file in
  the same directory and renaming it into place.

## Probes

| Probe | Expected | Observed on 2.85 | Service design | Ubuntu CI version |
| --- | --- | --- | --- | --- |
| `TestMockConfigAnswersLocalNames` | Exact `address=` names, `.home.example.test` wildcards and `.zip` answer locally. | As expected: `nas.home.example.test` is 10.0.0.40, any other name under the wildcard is 10.0.0.17, `example.zip` is 127.0.0.1. | The mock config is a usable stand-in for the live file. More specific `address=` entries beat the wildcard. | |
| `TestHostsDirRecordBeatsWildcardWithoutReload` | A hosts-directory record overrides the `address=/.home.example.test/` wildcard, with no SIGHUP. | As expected: the answer changes from 10.0.0.17 to 10.0.0.80 as soon as inotify reads the file. | The service can add names under `<private-zone>` by writing the hosts directory alone; the wildcard does not shadow them. | |
| `TestHostsDirUpdateKeepsOldAddressUntilReload` (originally `TestHostsDirUpdateWithoutReload`) | Changing a record's address replaces the old answer without a SIGHUP. | **Differs.** The new address is added but the old one is kept: the name answers both 10.0.0.80 and 10.0.0.81 until SIGHUP, after which only 10.0.0.81 is served. | An address change is not complete until the service sends SIGHUP; without it clients get both addresses. | |
| `TestHostsDirRemovalNeedsReload` | Emptying the file leaves the removed record served until SIGHUP. | As expected: 10.0.0.80 is still served two seconds after the file is emptied; after SIGHUP the name falls back to the wildcard (10.0.0.17). | Removing a record needs SIGHUP. | |

## What this means for the service

- **Precedence:** hosts-directory records beat `address=` wildcards, so a
  managed record for a name under a wildcard domain is served.
- **Additions** take effect without a signal.
- **Updates and removals** do not: dnsmasq 2.85 rereads a changed hosts file
  and adds its entries, but never drops the entries the previous version of
  the file contributed. Only SIGHUP (which clears the cache and rereads every
  hosts file) removes them. The simplest correct rule is to send SIGHUP after
  every write of the managed file.
- **Deleting the file** is not acted on either. A manual check in the probe
  image showed that `rm managed.hosts` logs nothing and the old records keep
  being served until SIGHUP. The service should empty the file rather than
  delete it, and signal either way.
- **Temporary files:** dnsmasq ignores dotfiles in the hosts directory. The
  logs show `inotify, new or changed file` only for `managed.hosts`, never
  for the `.managed.hosts.*` temporary file, so writing a dot-prefixed
  temporary file in the same directory and renaming it over the managed file
  is safe: dnsmasq never reads a partial file.

## How the changed probe was checked

The update probe failed with `last answer [10.0.0.80 10.0.0.81]`. To rule out
a harness bug, the same steps were repeated by hand in the probe image with a
shell script (`mv` of a temporary file, then an in-place overwrite) and
`--log-queries`:

- the file held only the new line (`10.0.0.81 app.home.example.test`);
- dnsmasq logged `read .../managed.hosts - 1 addresses` for each write, then
  answered the query with both addresses, each attributed to the hosts
  directory rather than the cache of an upstream answer;
- the doubled answer was still there three seconds later, so it is not a
  timing effect;
- after SIGHUP dnsmasq logged `cleared cache`, reread the file and answered
  only the new address.
