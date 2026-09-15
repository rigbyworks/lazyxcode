.PHONY: build install test check clean

BINARY := lazyxcode
VERSION ?= dev
BINDIR ?= $(HOME)/.local/bin

build:
	go build -trimpath -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

install: build
	install -d "$(BINDIR)"
	install -m 755 "$(BINARY)" "$(BINDIR)/$(BINARY)"

test:
	go test ./...

check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test -race ./...
	go build ./...

clean:
	rm -f "$(BINARY)"
