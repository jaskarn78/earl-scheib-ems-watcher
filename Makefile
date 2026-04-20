# Earl Scheib EMS Watcher — Build Makefile
#
# HMAC secret injection:
#   Set GSD_HMAC_SECRET env var before running build-windows.
#   If unset, the default dev token in main.go is used.
#   Example: GSD_HMAC_SECRET=mysecret make build-windows
#
# CI usage (GitHub Actions):
#   The workflow sets GSD_HMAC_SECRET from the repository secret GSD_HMAC_SECRET.

BINARY   := earlscheib.exe
MODULE   := github.com/jjagpal/earl-scheib-watcher
VERSION  ?= dev
LDFLAGS  := -s -w
HMAC_SECRET ?= $(GSD_HMAC_SECRET)

# Inject HMAC secret if provided; fall back to in-source dev default otherwise.
# Use strip to ensure empty-string assignment does not trigger injection.
ifneq ($(strip $(HMAC_SECRET)),)
LDFLAGS += -X main.secretKey=$(HMAC_SECRET)
endif

.PHONY: build-windows build-linux clean

## build-windows: cross-compile windows/amd64 exe (CGO_ENABLED=0, Phase 1 only)
build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
	  go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY) ./cmd/earlscheib

## build-linux: build linux/amd64 binary for local testing
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	  go build -ldflags "$(LDFLAGS)" -o dist/earlscheib ./cmd/earlscheib

## clean: remove build artifacts
clean:
	rm -rf dist/

## help: list targets
help:
	@grep -E '^##' Makefile | sed 's/## //'
