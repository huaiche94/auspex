# Auspex build/lint/test targets.
#
# Taskfile.yml (https://taskfile.dev) is the primary, richer task runner for
# this repository; this Makefile is a thin, dependency-free mirror of the
# same targets for contributors and CI steps that only have `make`
# available. Keep the two in sync: a target added to one should be added to
# the other with equivalent behavior.

BIN := auspex
PKGS := ./...

.PHONY: all build run test test-short fmt fmt-fix vet lint tidy clean

all: fmt lint test

build:
	mkdir -p bin
	go build -o bin/$(BIN) ./cmd/auspex

run: build
	./bin/$(BIN) $(ARGS)

test:
	go test -race $(PKGS)

test-short:
	go test $(PKGS)

fmt:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt would reformat:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

fmt-fix:
	gofmt -w .

vet:
	go vet $(PKGS)

lint: vet
	golangci-lint run $(PKGS)

tidy:
	go mod tidy
	@if [ -n "$$(git status --porcelain go.mod go.sum)" ]; then \
		echo "go.mod/go.sum are not tidy; run 'make tidy' and commit the result."; \
		git diff go.mod go.sum; \
		exit 1; \
	fi

clean:
	rm -rf bin
