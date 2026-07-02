# Assumes `go` is on PATH (production machine). In the build sandbox, first
# `source /tmp/goenv.sh` to put the staged Go 1.18 toolchain on PATH.
.PHONY: build test vet fmt tidy clean
build:
	go build -o bin/reponite ./cmd/reponite
test:
	go test ./...
vet:
	go vet ./...
fmt:
	gofmt -l -w .
tidy:
	go mod tidy
clean:
	rm -rf bin

# Build/test the SQLite adapter (needs network for the module once):
sqlite:
	go get modernc.org/sqlite
	go build -tags sqlite ./...
	go test -tags sqlite ./internal/storage/sqlite/

# Build/test the tree-sitter parser adapter (CGO; needs network for the module once):
treesitter:
	go get github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang
	go build -tags treesitter ./...
	go test -tags treesitter ./internal/processing/

# Build the full index-backed CLI (needs network for modules once):
cli:
	go get modernc.org/sqlite github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang
	go build -tags "sqlite treesitter" -o bin/reponite ./cmd/reponite

e2e:
	go get modernc.org/sqlite github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang
	go test -tags "sqlite treesitter" ./internal/e2e/
