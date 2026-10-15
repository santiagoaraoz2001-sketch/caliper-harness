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
	Format                              string
	Complete                            bool
	headerFound                         bool
}

// LoadCandidate is metadata admission only; the native server checks compatibility.
func (a Asset) LoadCandidate() bool {
	return a.Complete || a.Format == "gguf" && a.headerFound
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
		if strings.EqualFold(filepath.Ext(canonical), ".gguf") {
			a.Format, a.Runtime = "gguf", ""
			f, err := os.Open(canonical)
			if err == nil {
				var magic [4]byte
				_, err = io.ReadFull(f, magic[:])
				f.Close()
				if err == nil && string(magic[:]) == "GGUF" {
					a.headerFound = true // Not checkpoint completeness.
				} else {
					err = fmt.Errorf("missing GGUF header")
				}
			}
			a.Reason = "GGUF header found; checkpoint completeness and native compatibility unconfirmed"
			if err != nil {
				a.Reason = err.Error()
			}
		} else if runtime == "ollama" {
			a.Reason = "native catalog directory; model identities require its server"
		} else {
			a.Format, a.Runtime = "safetensors", ""
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
	candidate := func(path, id, rev string) {
		st, err := os.Stat(path)
		if err != nil || !st.IsDir() {
			add(path, id, rev, "")
			return
		}
		found := false
		for _, entry := range dirs(path) {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
				add(filepath.Join(path, entry.Name()), id+"/"+entry.Name(), rev, "")
				found = true
			}
		}
		if !found {
			add(path, id, rev, "")
			return
		}
		for _, weights := range []string{"model.safetensors", "model.safetensors.index.json"} {
			if _, err := os.Stat(filepath.Join(path, weights)); err == nil {
				add(path, id, rev, "")
				break
			}
		}
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
		if st, err := os.Stat(root.Path); err == nil && !st.IsDir() && strings.EqualFold(filepath.Ext(root.Path), ".gguf") {
			candidate(root.Path, filepath.Base(root.Path), "")
			continue
		}
		if _, err := os.Stat(filepath.Join(root.Path, "config.json")); err == nil {
			candidate(root.Path, filepath.Base(root.Path), "")
			continue
		}
		for _, entry := range dirs(root.Path) {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".gguf") {
				add(filepath.Join(root.Path, entry.Name()), entry.Name(), "", "openai")
				continue
			}
			if !entry.IsDir() {
				continue
			}
			p := filepath.Join(root.Path, entry.Name())
			if root.Kind == "hf" && strings.HasPrefix(entry.Name(), "models--") {
				for _, rev := range dirs(filepath.Join(p, "snapshots")) {
					if rev.IsDir() {
						candidate(filepath.Join(p, "snapshots", rev.Name()), strings.ReplaceAll(strings.TrimPrefix(entry.Name(), "models--"), "--", "/"), rev.Name())
					}
				}
			} else {
				candidate(p, entry.Name(), "")
			}
		}
	}
	for _, p := range explicit {
		if p != "" {
			candidate(p, filepath.Base(p), "")
		}
	}
	return out, warnings
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
