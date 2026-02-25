.PHONY: build test clean

BINARY_NAME=tenazas

build:
	mkdir -p bin
	go build -o bin/$(BINARY_NAME) .

test:
	go test -v ./...

clean:
	rm -f $(BINARY_NAME)
	go clean

check-test-integrity:
	git diff --exit-code *_test.go || (echo 'CHEAT DETECTED: You modified the tests!' >&2 && git checkout *_test.go && exit 1)