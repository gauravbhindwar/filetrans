BINARY   := filetrans
CMD      := ./cmd/filetrans
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -trimpath -ldflags="-s -w -X main.version=$(VERSION)"

.PHONY: all dev windows linux linux-arm darwin darwin-arm clean tidy vet test

all: windows linux linux-arm darwin darwin-arm

# Build for current OS/arch (fastest for development)
dev:
	go build $(LDFLAGS) -o dist/$(BINARY) $(CMD)
	@echo "Built: dist/$(BINARY)  ($(shell go env GOOS)/$(shell go env GOARCH))"

windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)_windows_amd64.exe $(CMD)
	@echo "Built: dist/$(BINARY)_windows_amd64.exe"

linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)_linux_amd64 $(CMD)
	@echo "Built: dist/$(BINARY)_linux_amd64"

linux-arm:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)_linux_arm64 $(CMD)
	@echo "Built: dist/$(BINARY)_linux_arm64"

darwin:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BINARY)_darwin_amd64 $(CMD)
	@echo "Built: dist/$(BINARY)_darwin_amd64"

darwin-arm:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BINARY)_darwin_arm64 $(CMD)
	@echo "Built: dist/$(BINARY)_darwin_arm64"

checksums: all
	cd dist && sha256sum * > checksums.txt

vet:
	go vet ./...

test:
	go test ./... -timeout 60s -race

tidy:
	go mod tidy

clean:
	rm -rf dist/
