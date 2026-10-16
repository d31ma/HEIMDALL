# Vendored: CHEX Go client

Source: `CHEX/clients/go/chex.go` (https://github.com/d31ma/CHEX)

CHEX is a Rust project. Its Go shim is a single stdlib-only file that is not
published as an importable Go module, so it is copied verbatim rather than
imported. Do not edit it here — change it upstream and re-run
`scripts/vendor-clients.sh`.

The file drives the `chex` executable over NDJSON on stdin/stdout. The binary
must be on PATH or named by `CHEX_BINARY`.
