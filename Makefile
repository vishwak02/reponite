.PHONY: build test vet fmt tidy clean cli sqlite treesitter mcp e2e

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

# Tree-sitter grammars (all subpackages of one module) shared by cli/treesitter/e2e:
GRAMMARS = github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang github.com/smacker/go-tree-sitter/python github.com/smacker/go-tree-sitter/javascript github.com/smacker/go-tree-sitter/typescript/typescript github.com/smacker/go-tree-sitter/typescript/tsx github.com/smacker/go-tree-sitter/java github.com/smacker/go-tree-sitter/c github.com/smacker/go-tree-sitter/cpp github.com/smacker/go-tree-sitter/rust

# Version stamped into the binary (git tag/commit; falls back to the source default):
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/vishwak02/reponite/internal/version.Version=$(VERSION)

# Full CLI with all adapters (-mod=mod self-heals go.sum for tag-gated deps):
cli:
	go get modernc.org/sqlite $(GRAMMARS) github.com/mark3labs/mcp-go github.com/fsnotify/fsnotify github.com/go-git/go-git/v5 golang.org/x/tools
	go build -mod=mod -tags "sqlite treesitter mcp" -ldflags "$(LDFLAGS)" -o bin/reponite ./cmd/reponite

# Individual adapter checks (mirror CI):
sqlite:
	go get modernc.org/sqlite
	go build -mod=mod -tags sqlite ./... && go test -mod=mod -tags sqlite ./internal/storage/sqlite/
treesitter:
	go get $(GRAMMARS) github.com/go-git/go-git/v5 golang.org/x/tools
	go build -mod=mod -tags treesitter ./... && go test -mod=mod -tags treesitter ./internal/processing/
mcp:
	go get modernc.org/sqlite github.com/mark3labs/mcp-go
	go build -mod=mod -tags "sqlite mcp" ./...
e2e:
	go get modernc.org/sqlite $(GRAMMARS) github.com/fsnotify/fsnotify github.com/go-git/go-git/v5 golang.org/x/tools
	go test -mod=mod -tags "sqlite treesitter" ./internal/e2e/
