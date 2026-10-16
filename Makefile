.PHONY: build test vet bundle-macos bundle-windows

build:
	go build -o caliper-harness ./cmd/specific-harness

test:
	go test ./...

vet:
	go vet ./...

# Explicit output directory; refuses an existing nonempty folder.
bundle-macos:
	./scripts/bundle-macos.sh "$(OUT)"

# Offline Windows x64 developer builder: Go, mingw GCC, Bash and ripgrep.
# Refuses every existing output. Private runtime acquisition uses
# scripts/bundle-windows-managed.ps1 -Out NEW_FOLDER -AllowDownloads.
bundle-windows:
	powershell -ExecutionPolicy Bypass -File scripts/bundle-windows.ps1 "$(OUT)"
