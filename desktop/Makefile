# UniformTypeIdentifiers: wails v2.13 references UTType; plain go build does not link it by default
CGO_LDFLAGS := -framework UniformTypeIdentifiers
# default to the installed CLI's version so attach handshakes match out of the box
VERSION ?= $(shell choragos version 2>&1 | awk '/^choragos/{print $$2}')
ifeq ($(VERSION),)
VERSION := dev
endif

.PHONY: build
build:
	CGO_LDFLAGS="$(CGO_LDFLAGS)" go build -tags desktop,production \
		-ldflags "-X main.version=$(VERSION)" -o build/choragos-desktop .

.PHONY: vet
vet:
	go vet ./...
