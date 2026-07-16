# UniformTypeIdentifiers: wails v2.13 references UTType; plain go build does not link it by default
CGO_LDFLAGS := -framework UniformTypeIdentifiers
VERSION ?= dev

.PHONY: build
build:
	CGO_LDFLAGS="$(CGO_LDFLAGS)" go build -tags desktop,production \
		-ldflags "-X main.version=$(VERSION)" -o build/choragos-desktop .

.PHONY: vet
vet:
	go vet ./...
