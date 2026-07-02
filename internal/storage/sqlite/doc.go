// Package sqlite is the production Store: a pure-Go SQLite backend
// (modernc.org/sqlite, no CGO) implementing query.Store. The implementation is
// behind the `sqlite` build tag, so the default build stays dependency-free and
// compiles where the module proxy is unavailable (ADR-018, ADR-001). Build and
// test it with the tag:
//
//	go get modernc.org/sqlite
//	go build -tags sqlite ./...
//	go test  -tags sqlite ./internal/storage/sqlite/
//
// or `make sqlite`. This file (untagged) exists only so the package has a
// buildable file in the default build.
package sqlite
