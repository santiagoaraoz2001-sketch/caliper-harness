package serve

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Assets are metadata candidates, never compatibility or resident proof.
type Asset struct {
	Path, ID, Revision, Runtime, Reason string
	Complete                            bool
}
type ModelRoot struct{ Path, Kind string }

// ModelRoots honors native cache overrides; explicit directories can be appended.
func ModelRoots(home string) []ModelRoot {
	hf := os.Getenv("HF_HUB_CACHE")
	if hf == "" {
		hf = os.Getenv("HUGGINGFACE_HUB_CACHE")
	}
	if hf == "" {
		base := os.Getenv("HF_HOME")
		if base == "" {
			base = filepath.Join(home, ".cache", "huggingface")
			if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
				base = filepath.Join(x, "huggingface")
			}
		}
		hf = filepath.Join(base, "hub")
	}
	ollama := os.Getenv("OLLAMA_MODELS")
	if ollama == "" {
		ollama = filepath.Join(home, ".ollama", "models")
	}
	return []ModelRoot{{hf, "hf"}, {ollama, "ollama"}}
}

// Discover visits only supplied roots and immediate native snapshot layouts.
// At most 4096 directory entries total and 1024 candidate paths: exhaustion is visible.
// Missing optional roots are absent; malformed/inaccessible candidates are visible.
func Discover(roots []ModelRoot, explicit []string) ([]Asset, []string) {
	var out []Asset
	var warnings []string
	seen := map[string]bool{}
	inspected := 0
	remaining := 4096
	exhausted := false
	add := func(path, id, rev, runtime string) {
		inspected++
		if inspected > 1024 {
			if !exhausted {
				warnings = append(warnings, "metadata admission limit reached (4096 entries / 1024 candidate paths)")
				exhausted = true
			}
			return
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err == nil {
			canonical, err = filepath.Abs(canonical)
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			return
		}
		if seen[canonical] {
			return
		}
		seen[canonical] = true
		a := Asset{Path: canonical, ID: id, Revision: rev, Runtime: runtime}
		if runtime == "ollama" {
			a.Reason = "native catalog directory; model identities require its server"
		} else if runtime == "llama" {
			if st, err := os.Stat(canonical); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
				a.Complete = true
				a.Reason = "GGUF weights present; load starts llama-server with this file"
			} else {
				a.Reason = "missing/empty GGUF file"
			}
		} else {
			if err := localComplete(canonical); err != nil {
				a.Reason = err.Error()
			} else {
				a.Complete = true
				a.Reason = "metadata complete; native compatibility unconfirmed"
			}
		}
		out = append(out, a)
	}
	dirs := func(path string) []os.DirEntry {
		if remaining <= 0 || exhausted {
			if !exhausted {
				warnings = append(warnings, "metadata admission limit reached (4096 entries / 1024 candidate paths)")
				exhausted = true
			}
			return nil
		}
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		defer f.Close()
		entries, err := f.ReadDir(remaining + 1)
		if err != nil && len(entries) == 0 && err != io.EOF {
			warnings = append(warnings, fmt.Sprintf("%s: %v", path, err))
		}
		if len(entries) > remaining {
			entries = entries[:remaining]
			warnings = append(warnings, "metadata admission limit reached (4096 entries / 1024 candidate paths)")
			exhausted = true
		}
		remaining -= len(entries)
		return entries
	}
	for _, root := range roots {
		if exhausted {
			break
		}
		if root.Path == "" {
			continue
		}
		if root.Kind == "ollama" {
			if st, err := os.Stat(root.Path); err == nil && st.IsDir() {
				add(root.Path, "native Ollama catalog", "", "ollama")
			} else if err != nil && !os.IsNotExist(err) {
				warnings = append(warnings, fmt.Sprintf("%s: %v", root.Path, err))
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(root.Path, "config.json")); err == nil {
			add(root.Path, filepath.Base(root.Path), "", checkpointRuntime())
			continue
		}
		for _, entry := range dirs(root.Path) {
			if !entry.IsDir() {
				if isGGUF(entry.Name()) {
					add(filepath.Join(root.Path, entry.Name()), ggufServedName(entry.Name()), "", "llama")
				}
				continue
			}
			p := filepath.Join(root.Path, entry.Name())
			if root.Kind == "hf" && strings.HasPrefix(entry.Name(), "models--") {
				for _, rev := range dirs(filepath.Join(p, "snapshots")) {
					if rev.IsDir() {
						snap := filepath.Join(p, "snapshots", rev.Name())
						add(snap, strings.ReplaceAll(strings.TrimPrefix(entry.Name(), "models--"), "--", "/"), rev.Name(), checkpointRuntime())
						for _, f := range dirs(snap) {
							if !f.IsDir() && isGGUF(f.Name()) {
								add(filepath.Join(snap, f.Name()), ggufServedName(f.Name()), rev.Name(), "llama")
							}
						}
					}
				}
			} else {
				add(p, entry.Name(), "", checkpointRuntime())
				for _, f := range dirs(p) {
					if !f.IsDir() && isGGUF(f.Name()) {
						add(filepath.Join(p, f.Name()), ggufServedName(f.Name()), "", "llama")
					}
				}
			}
		}
	}
	for _, p := range explicit {
		if p != "" {
			if isGGUF(filepath.Base(p)) {
				add(p, ggufServedName(filepath.Base(p)), "", "llama")
			} else {
				add(p, filepath.Base(p), "", checkpointRuntime())
			}
		}
	}
	return out, warnings
}

// AssetModelName is the wire identity a load/serve action uses for an
// asset row: GGUF-backed runtimes serve under the file's base name;
// checkpoint runtimes take the directory path itself.
func AssetModelName(a Asset) string {
	if a.Runtime == "rulith" || a.Runtime == "llama" {
		return ggufServedName(filepath.Base(a.Path))
	}
	return a.Path
}

func isGGUF(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".gguf")
}

// ggufServedName strips the extension and any -NNNNN-of-NNNNN shard
// suffix: the identity /v1/models reports for the file.
func ggufServedName(filename string) string {
	name := filename
	if isGGUF(name) {
		name = name[:len(name)-len(".gguf")]
	}
	// Shard suffix is exactly "-ddddd-of-ddddd" (15 chars).
	if len(name) > 15 {
		tail := name[len(name)-15:]
		digits := func(s string) bool {
			for _, c := range s {
				if c < '0' || c > '9' {
					return false
				}
			}
			return len(s) == 5
		}
		if tail[0] == '-' && tail[6:10] == "-of-" && digits(tail[1:6]) && digits(tail[10:15]) {
			name = name[:len(name)-15]
		}
	}
	return name
}

func metadata(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return decodeWhole(f, out)
}
func localComplete(path string) error {
	var config map[string]any
	if err := metadata(filepath.Join(path, "config.json"), &config); err != nil || len(config) == 0 {
		return fmt.Errorf("invalid/missing config.json: %v", err)
	}
	tokenizer := false
	for _, name := range []string{"tokenizer.json", "tokenizer.model"} {
		if st, err := os.Stat(filepath.Join(path, name)); err == nil && st.Mode().IsRegular() && st.Size() > 0 {
			tokenizer = true
		}
	}
	if !tokenizer {
		return fmt.Errorf("missing tokenizer.json/tokenizer.model")
	}
	var index struct {
		WeightMap map[string]string `json:"weight_map"`
	}
	if _, err := os.Stat(filepath.Join(path, "model.safetensors.index.json")); err == nil {
		if err := metadata(filepath.Join(path, "model.safetensors.index.json"), &index); err != nil || len(index.WeightMap) == 0 {
			return fmt.Errorf("invalid/empty weight index: %v", err)
		}
		for _, name := range index.WeightMap {
			if filepath.IsAbs(name) || filepath.Clean(name) == ".." || strings.HasPrefix(filepath.Clean(name), ".."+string(filepath.Separator)) {
				return fmt.Errorf("weight shard escapes candidate: %q", name)
			}
			if st, err := os.Stat(filepath.Join(path, name)); err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
				return fmt.Errorf("missing/empty weight shard %q", name)
			}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	st, err := os.Stat(filepath.Join(path, "model.safetensors"))
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return fmt.Errorf("missing/empty model.safetensors or weight index")
	}
	return nil
}
