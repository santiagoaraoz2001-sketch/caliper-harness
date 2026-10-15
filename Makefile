.PHONY: build test vet bundle-macos

build:
	go build -o caliper-harness ./cmd/specific-harness

test:
	go test ./...

vet:
	go vet ./...

# Explicit output directory; refuses an existing nonempty folder.
bundle-macos:
	./scripts/bundle-macos.sh "$(OUT)"
