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

# Full CLI with all adapters (-mod=mod self-heals go.sum for tag-gated deps):
cli:
	go get modernc.org/sqlite github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang github.com/mark3labs/mcp-go github.com/fsnotify/fsnotify github.com/go-git/go-git/v5 golang.org/x/tools
	go build -mod=mod -tags "sqlite treesitter mcp" -o bin/reponite ./cmd/reponite

# Individual adapter checks (mirror CI):
sqlite:
	go get modernc.org/sqlite
	go build -mod=mod -tags sqlite ./... && go test -mod=mod -tags sqlite ./internal/storage/sqlite/
treesitter:
	go get github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang github.com/go-git/go-git/v5 golang.org/x/tools
	go build -mod=mod -tags treesitter ./... && go test -mod=mod -tags treesitter ./internal/processing/
mcp:
	go get modernc.org/sqlite github.com/mark3labs/mcp-go
	go build -mod=mod -tags "sqlite mcp" ./...
e2e:
	go get modernc.org/sqlite github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang github.com/fsnotify/fsnotify github.com/go-git/go-git/v5 golang.org/x/tools
	go test -mod=mod -tags "sqlite treesitter" ./internal/e2e/
