package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// GitHub release assets must be under 2 GiB. Large whole-product archives are
// transported in independently hashed parts, then verified again as one archive.
// This is only a transport format: installation and runtime remain unchanged.
type ArchiveParts struct {
	Format  int           `json:"format"`
	Archive string        `json:"archive"`
	SHA256  string        `json:"sha256"`
	Parts   []ArchivePart `json:"parts"`
}
type ArchivePart struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func (m Manager) downloadArchive(ctx context.Context, r Release, dst io.Writer, want string) error {
	if r.ArchiveURL != "" {
		response, e := m.get(ctx, r.ArchiveURL)
		if e != nil {
			return e
		}
		defer response.Body.Close()
		_, e = io.Copy(dst, response.Body)
		return e
	}
	response, e := m.get(ctx, r.PartsURL)
	if e != nil {
		return e
	}
	var manifest ArchiveParts
	e = json.NewDecoder(response.Body).Decode(&manifest)
	response.Body.Close()
	if e != nil {
		return e
	}
	archive := "caliper-" + r.Version + "-" + m.Platform + ".tar.gz"
	if manifest.Format != 1 || manifest.Archive != archive || !strings.EqualFold(manifest.SHA256, want) || len(manifest.Parts) == 0 {
		return fmt.Errorf("invalid split-archive manifest")
	}
	base, e := url.Parse(r.PartsURL)
	if e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, part := range manifest.Parts {
		digest, e := hex.DecodeString(part.SHA256)
		if e != nil || len(digest) != sha256.Size || part.Bytes <= 0 || path.Base(part.Name) != part.Name || strings.ContainsAny(part.Name, "\\:") || !strings.HasPrefix(part.Name, archive+".part-") || seen[part.Name] {
			return fmt.Errorf("invalid archive part %q", part.Name)
		}
		seen[part.Name] = true
		href := *base
		href.Path = path.Join(path.Dir(base.Path), part.Name)
		href.RawPath = ""
		href.RawQuery = ""
		href.Fragment = ""
		response, e := m.get(ctx, href.String())
		if e != nil {
			return e
		}
		hash := sha256.New()
		n, e := io.Copy(io.MultiWriter(dst, hash), response.Body)
		response.Body.Close()
		if e != nil {
			return e
		}
		if n != part.Bytes || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), part.SHA256) {
			return fmt.Errorf("archive part %s failed size/checksum verification", part.Name)
		}
	}
	return nil
}
