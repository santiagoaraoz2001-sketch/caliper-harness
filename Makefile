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

# Windows x64 builder (Go + mingw gcc + uv); same output rule as bundle-macos.
bundle-windows:
	powershell -ExecutionPolicy Bypass -File scripts/bundle-windows.ps1 "$(OUT)"
