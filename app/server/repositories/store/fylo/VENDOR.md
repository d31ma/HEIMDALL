# Vendored: FYLO Go client

Source: `FYLO/clients/go/fylo.go` (https://fylo.del.ma)

FYLO is a Rust project. Its Go shim is a single stdlib-only file that is not
published as an importable Go module, so it is copied verbatim rather than
imported. Do not edit it here — change it upstream and re-run
`scripts/vendor-clients.sh`, so the two never diverge.

The file drives the `fylo` executable over NDJSON on stdin/stdout. The binary
must be on PATH or named by `FYLO_BINARY`.

Contrast with SESAME, which *is* a Go module and is a `go get` dependency: it
owns security state, and a vendored copy of a security client is a copy that
can silently fall behind the engine it talks to.
