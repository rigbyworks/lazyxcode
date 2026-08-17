.PHONY: build install test check clean

BINARY := lazy-xcode
BINDIR ?= $(HOME)/.local/bin

build:
	go build -trimpath -o $(BINARY) .

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
