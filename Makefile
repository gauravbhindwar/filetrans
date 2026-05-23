BINARY   := filetrans
CMD      := ./cmd/filetrans
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -trimpath -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: all dev binaries \
        linux linux-arm linux-armv7 linux-arm \
        windows windows-arm \
        darwin darwin-arm \
        packages deb rpm arch appimage tarballs macos-pkg \
        clean tidy vet test checksums

# ── binaries ──────────────────────────────────────────────────────────────────

all: binaries

binaries: linux linux-arm linux-armv7 windows windows-arm darwin darwin-arm

dev:
	@mkdir -p dist
	go build $(LDFLAGS) -o dist/$(BINARY) $(CMD)
	@echo "Built: dist/$(BINARY)  ($(shell go env GOOS)/$(shell go env GOARCH))"

linux:
	@mkdir -p dist
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)_linux_amd64 $(CMD)
	@echo "Built: dist/$(BINARY)_linux_amd64"

linux-arm:
	@mkdir -p dist
	GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)_linux_arm64 $(CMD)
	@echo "Built: dist/$(BINARY)_linux_arm64"

linux-armv7:
	@mkdir -p dist
	GOOS=linux  GOARCH=arm GOARM=7 go build $(LDFLAGS) -o dist/$(BINARY)_linux_arm $(CMD)
	@echo "Built: dist/$(BINARY)_linux_arm"

windows:
	@mkdir -p dist
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)_windows_amd64.exe $(CMD)
	@echo "Built: dist/$(BINARY)_windows_amd64.exe"

windows-arm:
	@mkdir -p dist
	GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)_windows_arm64.exe $(CMD)
	@echo "Built: dist/$(BINARY)_windows_arm64.exe"

darwin:
	@mkdir -p dist
	GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)_darwin_amd64 $(CMD)
	@echo "Built: dist/$(BINARY)_darwin_amd64"

darwin-arm:
	@mkdir -p dist
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)_darwin_arm64 $(CMD)
	@echo "Built: dist/$(BINARY)_darwin_arm64"

# ── packages ──────────────────────────────────────────────────────────────────
# Requires nfpm: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest

packages: deb rpm arch tarballs
	@echo "All Linux packages built in dist/"

deb: linux linux-arm
	VERSION=$(VERSION) ARCH=amd64  GOARCH=amd64 nfpm package --config packaging/nfpm.yaml --packager deb --target dist/
	VERSION=$(VERSION) ARCH=arm64  GOARCH=arm64 nfpm package --config packaging/nfpm.yaml --packager deb --target dist/

rpm: linux linux-arm
	VERSION=$(VERSION) ARCH=amd64  GOARCH=amd64 nfpm package --config packaging/nfpm.yaml --packager rpm --target dist/
	VERSION=$(VERSION) ARCH=arm64  GOARCH=arm64 nfpm package --config packaging/nfpm.yaml --packager rpm --target dist/

arch: linux linux-arm
	VERSION=$(VERSION) ARCH=amd64  GOARCH=amd64 nfpm package --config packaging/nfpm.yaml --packager archlinux --target dist/
	VERSION=$(VERSION) ARCH=arm64  GOARCH=arm64 nfpm package --config packaging/nfpm.yaml --packager archlinux --target dist/

tarballs: linux linux-arm linux-armv7
	tar -czf dist/$(BINARY)_linux_amd64.tar.gz  -C dist $(BINARY)_linux_amd64
	tar -czf dist/$(BINARY)_linux_arm64.tar.gz  -C dist $(BINARY)_linux_arm64
	tar -czf dist/$(BINARY)_linux_arm.tar.gz    -C dist $(BINARY)_linux_arm
	@echo "Built Linux tarballs"

# macOS packages — run on macOS only
macos-pkg: darwin darwin-arm
	bash scripts/package-macos.sh $(VERSION) amd64
	bash scripts/package-macos.sh $(VERSION) arm64

# ── quality ───────────────────────────────────────────────────────────────────

vet:
	go vet ./...

test:
	go test ./... -timeout 60s -race

tidy:
	go mod tidy

checksums:
	cd dist && sha256sum * > checksums.txt && cat checksums.txt

# ── cleanup ───────────────────────────────────────────────────────────────────

clean:
	rm -rf dist/ AppDir/ appimagetool
