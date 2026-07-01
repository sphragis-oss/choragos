.PHONY: build test run vet fmt lint vulncheck install uninstall

PREFIX ?= /usr/local

build:
	go build -o choragos ./cmd/choragos

test:
	go test ./...

run: build
	./choragos serve

demo: build
	./choragos serve --config examples/demo.toml --sphragis=false

vet:
	go vet ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run

vulncheck:
	govulncheck ./...

install: build
	install -d $(PREFIX)/bin
	install -m 0755 choragos $(PREFIX)/bin/choragos

uninstall:
	rm -f $(PREFIX)/bin/choragos
