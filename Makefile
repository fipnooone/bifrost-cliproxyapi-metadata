SHELL := /bin/bash
.DEFAULT_GOAL := help
.PHONY: help prepare test test-race build validate native-test native-test-race native-build host-smoke release-files test-stdlib test-stdlib-race

help:
	@printf '%s\n' 'make prepare       fetch and verify the pinned Bifrost dependency graph' 'make test          unit tests in the pinned Linux/amd64 build container' 'make test-race     race tests in that same container' 'make build         native .so in dist/' 'make validate      unit + race + build + actual Bifrost-image smoke' 'make test-stdlib   offline portable tests ONLY (not SDK/ABI validation)'
prepare:
	./scripts/prepare.sh
# Public build/test targets use the same compiler/container as release CI.
test:
	./scripts/container.sh make native-test
test-race:
	./scripts/container.sh make native-test-race
build:
	./scripts/container.sh make native-build
validate:
	./scripts/container.sh make native-test native-test-race native-build
	$(MAKE) host-smoke
native-test: prepare
	./scripts/go.sh test -count=1 ./...
native-test-race: prepare
	./scripts/go.sh test -race -count=1 ./...
native-build: prepare
	./scripts/build.sh
host-smoke:
	python3 scripts/smoke.py
release-files:
	./scripts/release-files.sh "$(VERSION)"
# Explicit filenames compile the real stdlib-only production code, NOT a fake
# Bifrost SDK. This escape hatch is not used by CI/release acceptance gates.
PORTABLE_FILES := config.go metadata.go client.go config_test.go metadata_test.go client_test.go
test-stdlib:
	GO111MODULE=off GOWORK=off GOFLAGS= GOTOOLCHAIN=local go test -count=1 -v $(PORTABLE_FILES)
test-stdlib-race:
	GO111MODULE=off GOWORK=off GOFLAGS= GOTOOLCHAIN=local CGO_ENABLED=1 go test -race -count=1 $(PORTABLE_FILES)
