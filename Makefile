.PHONY: build vet test tidy run setup clean

build:
	go build -trimpath -ldflags "-s -w" -o mcp-ms-graph ./cmd/mcp-ms-graph

vet:
	go vet ./...

test:
	go test ./...

tidy:
	go mod tidy

run: build
	./mcp-ms-graph

setup: build
	./mcp-ms-graph setup

clean:
	rm -f mcp-ms-graph
	rm -rf dist/
