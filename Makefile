.PHONY: build install test check clean smoke terminal-smoke

BINARY := lazyxcode
VERSION ?= dev
BINDIR ?= $(HOME)/.local/bin
export VERSION

build:
	bash scripts/build.sh

install: build
	install -d "$(BINDIR)"
	install -m 755 "$(BINARY)" "$(BINDIR)/$(BINARY)"

test:
	swift test

check:
	swift format lint --strict --recursive Sources Tests Package.swift
	swift test
	swift build
	bash scripts/test-cli.sh

smoke:
	LAZYXCODE_RELEASE_SMOKE=1 swift test --filter releaseSmoke

terminal-smoke:
	swift build
	python3 scripts/test-terminal.py "$$(swift build --show-bin-path)/lazyxcode"

clean:
	swift package clean
	rm -f "$(BINARY)"
