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

# Full CLI with all adapters (fetches modules once; needs a C toolchain for tree-sitter):
cli:
	go get modernc.org/sqlite github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang github.com/mark3labs/mcp-go
	go build -tags "sqlite treesitter mcp" -o bin/reponite ./cmd/reponite

# Individual adapter checks (mirror CI):
sqlite:
	go get modernc.org/sqlite
	go build -tags sqlite ./... && go test -tags sqlite ./internal/storage/sqlite/
treesitter:
	go get github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang
	go build -tags treesitter ./... && go test -tags treesitter ./internal/processing/
mcp:
	go get modernc.org/sqlite github.com/mark3labs/mcp-go
	go build -tags "sqlite mcp" ./...
e2e:
	go get modernc.org/sqlite github.com/smacker/go-tree-sitter github.com/smacker/go-tree-sitter/golang
	go test -tags "sqlite treesitter" ./internal/e2e/
