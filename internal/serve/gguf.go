package serve

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

var splitGGUF = regexp.MustCompile(`^(.*)-(\d{5})-of-(\d{5})\.gguf$`)

// GGUF file names identify llama.cpp split sets. Validate every declared file
// header; tensor integrity and runtime compatibility still belong to the server.
func inspectGGUF(path string) (string, error) {
	paths := []string{path}
	parts := splitGGUF.FindStringSubmatch(filepath.Base(path))
	if parts != nil {
		index, _ := strconv.Atoi(parts[2])
		count, _ := strconv.Atoi(parts[3])
		if count < 1 || index < 1 || index > count {
			return "", fmt.Errorf("invalid split GGUF index: %s", path)
		}
		paths = nil
		for i := 1; i <= count; i++ {
			paths = append(paths, filepath.Join(filepath.Dir(path), fmt.Sprintf("%s-%05d-of-%05d.gguf", parts[1], i, count)))
		}
	}
	for _, name := range paths {
		f, err := os.Open(name)
		if err != nil {
			return "", fmt.Errorf("missing/unreadable GGUF shard %s: %w", name, err)
		}
		var magic [4]byte
		_, err = io.ReadFull(f, magic[:])
		f.Close()
		if err != nil || string(magic[:]) != "GGUF" {
			return "", fmt.Errorf("invalid GGUF header: %s", name)
		}
	}
	return fmt.Sprintf("%d GGUF file headers present; tensor integrity and runtime compatibility unconfirmed", len(paths)), nil
}
