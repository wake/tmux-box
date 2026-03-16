# Makefile
.PHONY: build test lint clean

BIN := bin/tbox

build:
	go build -o $(BIN) ./cmd/tbox

test:
	go test -race -count=1 ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
