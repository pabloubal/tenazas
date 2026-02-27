.PHONY: build test clean

BINARY_NAME=tenazas

build:
	mkdir -p bin
	go build -o bin/$(BINARY_NAME) ./cmd/tenazas/

test:
	go test -v ./...

clean:
	rm -f bin/$(BINARY_NAME)
	go clean
