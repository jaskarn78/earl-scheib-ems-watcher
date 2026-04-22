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
VERSION  ?= 0.1.0-dev
LDFLAGS  := -s -w -X main.appVersion=$(VERSION)
HMAC_SECRET ?= $(GSD_HMAC_SECRET)

# Self-update cooldown (internal/update/update.go): default 120s (testing
# cadence — see OH4-05 plan). For production GA, either bump the default
# in internal/update/update.go or migrate to a string-based ldflags
# override (-ldflags -X accepts strings only; would need init() Atoi).
# Current production-raise path is: edit the int64 default directly.

# Inject HMAC secret if provided; fall back to in-source dev default otherwise.
# Use strip to ensure empty-string assignment does not trigger injection.
ifneq ($(strip $(HMAC_SECRET)),)
LDFLAGS += -X main.secretKey=$(HMAC_SECRET)
endif

.PHONY: build-windows build-linux test clean generate-resources install-tools dev-sign installer installer-syntax portable

## install-tools: install required build tools (go-winres)
install-tools:
	go install github.com/tc-hib/go-winres@v0.3.3

## generate-resources: generate Windows resource file (.syso) from winres/winres.json
## Requires go-winres: run `make install-tools` first.
## The .syso must live in cmd/earlscheib/ so go build picks it up automatically.
generate-resources:
	go-winres make --in winres/winres.json
	mv -f rsrc_windows_amd64.syso cmd/earlscheib/rsrc_windows_amd64.syso
	rm -f rsrc_windows_386.syso

## build-windows: cross-compile windows/amd64 exe (CGO_ENABLED=0, Phase 1 only)
build-windows: generate-resources
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
	  go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY) ./cmd/earlscheib

## build-linux: build linux/amd64 binary for local testing
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	  go build -ldflags "$(LDFLAGS)" -o dist/earlscheib ./cmd/earlscheib

## test: run all unit tests with race detector
## Note: -race requires CGO on Linux; CGO_ENABLED=0 is used only for cross-compile builds.
test:
	go test ./... -race -count=1

## clean: remove build artifacts
clean:
	rm -rf dist/ rsrc_windows_386.syso rsrc_windows_amd64.syso cmd/earlscheib/rsrc_windows_amd64.syso installer/Output/

## dev-sign: sign dist/earlscheib.exe with a self-signed cert (local testing only)
## Requires: openssl, osslsigncode  (sudo apt-get install openssl osslsigncode)
## Output: dist/earlscheib-signed.exe
dev-sign: build-windows
	@echo "Generating self-signed dev certificate..."
	openssl req -new -x509 -newkey rsa:2048 -keyout /tmp/dev-signing.key \
	  -out /tmp/dev-signing.crt -days 1 -nodes \
	  -subj "/CN=EarlScheibDevSign/O=DevOnly/C=US" 2>/dev/null
	openssl pkcs12 -export -out /tmp/dev-signing.pfx \
	  -inkey /tmp/dev-signing.key -in /tmp/dev-signing.crt \
	  -passout pass:devpass 2>/dev/null
	osslsigncode sign \
	  -pkcs12 /tmp/dev-signing.pfx \
	  -pass devpass \
	  -n "Earl Scheib EMS Watcher (DEV)" \
	  -i "https://support.jjagpal.me" \
	  -in dist/earlscheib.exe \
	  -out dist/earlscheib-signed.exe
	osslsigncode verify -in dist/earlscheib-signed.exe
	rm -f /tmp/dev-signing.key /tmp/dev-signing.crt /tmp/dev-signing.pfx
	@echo "Dev-signed artifact: dist/earlscheib-signed.exe"
	@echo "NOTE: self-signed cert -- SmartScreen will block this on Windows. For production use real OV cert."

## installer: build the Inno Setup installer exe using Docker (requires Docker, produces installer/Output/EarlScheibWatcher-Setup.exe)
## The binary at dist/earlscheib-artifact.exe must exist first (run make build-windows or use CI artifact).
## Uses amake/innosetup:latest — the pinned 6.7.1 tag is no longer published on Docker Hub.
installer:
	docker run --rm -v "$(CURDIR):/work" amake/innosetup:latest /work/installer/earlscheib.iss

## installer-syntax: parse-check the .iss script by running a full compile into a
## throwaway location. The amake/innosetup:latest image's entrypoint is iscc itself
## and only accepts a single filename argument, so there is no parse-only flag
## exposed here — a full compile acts as the syntax check.
installer-syntax:
	docker run --rm -v "$(CURDIR):/work" amake/innosetup:latest /work/installer/earlscheib.iss

## portable: build the portable zip distribution (requires zip binary; ubuntu: apt-get install zip)
## Produces dist/EarlScheibWatcher-Portable.zip containing:
##   earlscheib.exe, setup.cmd, uninstall.cmd, README.txt,
##   config.ini.template, tasks/*.xml
portable: build-windows
	@echo "Staging portable distribution..."
	rm -rf dist/portable-staging
	mkdir -p dist/portable-staging/tasks
	cp dist/$(BINARY)                                    dist/portable-staging/earlscheib.exe
	cp portable/setup.cmd                                dist/portable-staging/setup.cmd
	cp portable/uninstall.cmd                            dist/portable-staging/uninstall.cmd
	cp portable/README.txt                               dist/portable-staging/README.txt
	cp portable/config.ini.template                      dist/portable-staging/config.ini.template
	cp portable/tasks/EarlScheibEMSWatcher-SYSTEM.xml   dist/portable-staging/tasks/
	cp portable/tasks/EarlScheibEMSWatcher-User.xml      dist/portable-staging/tasks/
	@echo "Creating dist/EarlScheibWatcher-Portable.zip..."
	cd dist/portable-staging && zip -r ../EarlScheibWatcher-Portable.zip .
	rm -rf dist/portable-staging
	@echo "Portable zip: dist/EarlScheibWatcher-Portable.zip"

## help: list targets
help:
	@grep -E '^##' Makefile | sed 's/## //'
