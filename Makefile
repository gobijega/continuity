BINARY  := continuity
VERSION ?= 0.1.0-dev
PKG     := ./cmd/continuity

.PHONY: build run test cover vet fmt fmtcheck clean

build:
	go build -ldflags "-X main.Version=$(VERSION)" -o bin/$(BINARY) $(PKG)

run:
	go run $(PKG) --once

test:
	go test ./... -race -cover

cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -func=coverage.out

vet:
	go vet ./...

fmt:
	gofmt -w .

fmtcheck:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }

clean:
	rm -rf bin coverage.out
