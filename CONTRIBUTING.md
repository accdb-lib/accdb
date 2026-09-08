# Contributing to accdb

Thank you for your interest in contributing to `accdb`!

## Development Principles

1. **Pure Go**: Zero external runtime dependencies and standard library only (`database/sql`, `encoding/binary`, `math`, `os`, etc.). No CGO, no Java runtime in Go packages.
2. **Fail Fast and Explicitly**: Never return `nil` or report success if data was not written to disk. Return `ErrNotImplemented` for incomplete features.
3. **Defense Against Corrupted Input**: Binary parsers must never panic on malformed or malicious files. Use checked readers and bounds validation.
4. **Isolate Tests**: Always use `t.TempDir()` in tests. Never create `.accdb` files in the repository root.

## Local Testing

Run all unit tests and race checks:

```bash
go test -v ./...
go test -race ./...
go vet ./...
gofmt -l .
```

Run fuzz tests:

```bash
go test -fuzz=FuzzParseHeader -fuzztime=30s .
go test -fuzz=FuzzDecodeRow -fuzztime=30s .
```

## Pull Request Guidelines

- Ensure `go vet ./...`, `go test -race ./...`, and `gofmt` pass without warnings.
- Keep commits atomic and descriptive.
- Include unit tests for any bug fixes or new features.
