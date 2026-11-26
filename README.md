# cipd-meta-service

A small read-only, CIPD-compatible metadata service. It answers the requests
the `cipd` client makes to install packages (`ResolveVersion`, `GetInstanceURL`)
from a static `index.json` and serves package files from a local directory.
Intended for air-gapped or mirrored environments where the public CIPD backend
is not reachable.

**Status:** MVP. Verified with `cipd` 2.10.0 (depot_tools) for resolving by
ref, tag and instance ID, and for the not-found path. Only SHA-256 instances
are supported.

## Quick start

    go run ./cmd/cipd-meta-indexer -root ./store -file ./store/pkg.cipd \
      -name my/pkg/linux-amd64 -refs latest -tags version:1.0
    go run ./cmd/cipd-meta-service -root ./store

    cipd ensure -ensure-file ensure.txt -root ./out -service-url http://127.0.0.1:8099

## How it works

- `cipd-meta-indexer` hashes a `.cipd` file and records it, with refs and
  tags, in `index.json`.
- `cipd-meta-service` loads the index, resolves versions, and returns a
  download URL. Only files listed in the index are served.
- The index is reloaded periodically and on SIGHUP; a broken index never
  replaces a working one.

## Nexus mode

Instead of a local directory the service can read the index from a Nexus raw
repository and point clients at package files in another one:

    NEXUS_USER=reader NEXUS_PASSWORD=... cipd-meta-service \
      -nexus-index-url https://nexus.example/repository/cipd-index \
      -nexus-files-url https://nexus.example/repository/cipd-packages

The service only reads. Clients download package files from Nexus directly, so
the files repository must be readable by them (for example anonymously inside
the internal network). Use two repositories: package files never change
(forbid redeploy), the index does (allow redeploy).

## Mirroring

`cipd-meta-mirror` copies packages from the public CIPD backend into the store
and records them in the index. It needs internet access and write access, so
run it only in the sync zone.

    # one package; -dry-run first to see what would change
    cipd-meta-mirror -dry-run -package infra/tools/foo/linux-amd64 -version latest \
      -store-dir ./store

    # many packages: one "<package> <version>" per line
    NEXUS_USER=mirror NEXUS_PASSWORD=... cipd-meta-mirror -list packages.txt \
      -nexus-files-url https://nexus.example/repository/cipd-packages \
      -nexus-index-url https://nexus.example/repository/cipd-index

- Package files are written first, the index last.
- The version from DEPS is recorded as a tag or ref, so clients that ask for
  it find it. An instance ID creates no tag or ref.
- A tag that already points to another instance is an error. A ref is moved
  only with `-allow-move-ref`.
- The downloaded file's SHA-256 must match the instance ID that upstream
  resolved, otherwise nothing is written.
- Before every index change the previous index is saved as
  `snapshots/index-<time>.json` in the index store.
- Run one mirror at a time (in GitLab CI: `resource_group`).
- It drives the `cipd` client (`cipd resolve`, `cipd pkg-fetch`).

## Finding what is missing

When a client asks for a version that is not in the index, the service logs a
line `MISSING <package> <version>`. Turn the log into a mirror list:

    grep MISSING service.log | sed 's/.*MISSING //' | sort -u > missing.txt
    cipd-meta-mirror -list missing.txt -store-dir ./store

## Security notes

- Read-only: no registration, tags or ACL endpoints; they return Unimplemented.
- The index is validated on load: SHA-256 digests only, no absolute or
  parent-relative file paths, refs and tags must point to known instances.
- Files outside the index are never served, regardless of path tricks.
- Request bodies and index size are bounded; the HTTP server has timeouts.
- There is no authentication or TLS: run it behind a reverse proxy or on a
  trusted network.

## Limitations

- A tag maps to a single instance.
- Packages are mirrored one version at a time; there is no automatic
  discovery of what `DEPS` needs yet.
- `DescribeInstance`, `DescribeClient` and other methods are not implemented.

## License

Apache-2.0