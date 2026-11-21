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
- No Nexus backend yet; files come from a local directory.
- `DescribeInstance`, `DescribeClient` and other methods are not implemented.

## License

Apache-2.0