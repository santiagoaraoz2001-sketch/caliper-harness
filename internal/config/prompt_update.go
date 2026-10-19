package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"specific-harness/internal/prompts"
	"strings"
)

// PreserveCustomPrompts runs after preparing the new package, before activation.
// Explicit catalogs retain their path. An edited shipped catalog is copied
// byte-for-byte outside versioned program files and bound in workspace settings.
// Old versions and originals are never removed; incompatible catalogs refuse the
// update rather than silently reverting the user's instructions.
func PreserveCustomPrompts(configPath string, cfg Config, oldBundleRoot, newCatalogPath string) error {
	return preservePrompts(configPath, cfg, oldBundleRoot, newCatalogPath, true)
}

// PreserveInstalledPrompts is the machine-only activation path for package
// managers. Their working directory is not a user workspace.
func PreserveInstalledPrompts(oldBundleRoot, newCatalogPath string) error {
	cfg := Defaults()
	if err := loadConnections(&cfg); err != nil {
		return err
	}
	return preservePrompts("", cfg, oldBundleRoot, newCatalogPath, false)
}

func preservePrompts(configPath string, cfg Config, oldBundleRoot, newCatalogPath string, workspace bool) error {
	selected := cfg.Prompts.Path
	if selected == "" && cfg.Templates.Path != "" && cfg.Templates.Path != "templates" {
		selected = cfg.Templates.Path
		if !strings.HasSuffix(selected, ".yaml") && !strings.HasSuffix(selected, ".yml") {
			selected = filepath.Join(selected, "prompts.yaml")
		}
	}
	if selected != "" {
		return prompts.CompatibleWith(selected, newCatalogPath)
	}
	if oldBundleRoot == "" {
		return nil
	}
	oldPath := filepath.Join(oldBundleRoot, "prompts.yaml")
	raw, err := os.ReadFile(oldPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	manifest, err := os.ReadFile(filepath.Join(oldBundleRoot, "MANIFEST.sha256"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "  ", 2)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "./") == "prompts.yaml" && fields[0] == digest {
			return nil
		}
	}
	if err := prompts.CompatibleWith(oldPath, newCatalogPath); err != nil {
		return fmt.Errorf("custom prompts preserved at %s; update cannot activate: %w", oldPath, err)
	}
	root, err := UserDataDir()
	if err != nil {
		return err
	}
	path := filepath.Join(root, "prompts", digest+".yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) != string(raw) {
			return fmt.Errorf("preserved prompt copy differs at %s", path)
		}
	} else if os.IsNotExist(err) {
		if err := os.WriteFile(path, raw, 0600); err != nil {
			return err
		}
	} else {
		return err
	}
	cfg.Prompts.Path = path
	if workspace && configPath == "" {
		configPath = DefaultFile()
	}
	connections, err := ConnectionsPath()
	if err != nil {
		return err
	}
	originals := []string{connections}
	if workspace {
		originals = append(originals, configPath)
	}
	for _, original := range originals {
		if err := backupUpdateConfig(original); err != nil {
			return err
		}
	}
	if workspace {
		if err := SaveEffective(configPath, cfg); err != nil {
			return err
		}
	}
	// Editing the shipped catalog previously affected every workspace. Preserve
	// that machine-wide meaning while project-specific paths still override it.
	return saveConnections(cfg, &cfg.Prompts)
}

// Updates that bind preserved prompts retain exact pre-update settings. These
// are recovery files, not an automatic downgrade or a schema translation.
func backupUpdateConfig(path string) error {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := UserDataDir()
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(append([]byte(absolute+"\x00"), raw...)))
	dir := filepath.Join(root, "update-backups", digest)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for name, content := range map[string][]byte{"config.yaml": raw, "source.txt": []byte(absolute)} {
		target := filepath.Join(dir, name)
		if existing, err := os.ReadFile(target); err == nil {
			if string(existing) != string(content) {
				return fmt.Errorf("update backup differs: %s", target)
			}
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.WriteFile(target, content, 0600); err != nil {
			return err
		}
	}
	return nil
}
