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
