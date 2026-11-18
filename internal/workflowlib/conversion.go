package workflowlib

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Conversion drafts turn one external Markdown skill into Caliper's
// native structures. The draft records the full source text and the
// model-authored pieces mapped from it; publishing (domain graph nodes
// or library blocks/workflow) is performed by the owning tool layer.
// Nothing here enables or qualifies work.
type ConversionPiece struct {
	SourceHeading string `json:"source_heading"`
	Kind          string `json:"kind"` // section, example, or negative
	Title         string `json:"title"`
	Purpose       string `json:"purpose,omitempty"`
	Body          string `json:"body"`
	Evidence      string `json:"evidence,omitempty"`
	Links         string `json:"links,omitempty"` // one knowledge key or key#section per line
}

type Conversion struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	Target        string            `json:"target"` // domain or workflow
	SourcePath    string            `json:"source_path"`
	SourceSHA256  string            `json:"source_sha256"`
	SourceText    string            `json:"source_text"`
	Headings      []string          `json:"headings,omitempty"`
	Pieces        []ConversionPiece `json:"pieces,omitempty"`
	Status        string            `json:"status"` // draft or published
	PublishedKeys []string          `json:"published_keys,omitempty"`
	Revision      int64             `json:"revision"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

const (
	ConversionDraft     = "draft"
	ConversionPublished = "published"
)

// ParseSkillOutline extracts frontmatter name/description and level-2+
// ATX headings from a conventional Markdown skill. The first level-1
// heading is the document title, not a mappable section.
func ParseSkillOutline(text string) (name, description string, headings []string) {
	lines := strings.Split(text, "\n")
	inFront := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if i == 0 && trimmed == "---" {
			inFront = true
			continue
		}
		if inFront {
			if trimmed == "---" {
				inFront = false
				continue
			}
			if k, v, ok := strings.Cut(trimmed, ":"); ok {
				switch strings.TrimSpace(k) {
				case "name":
					name = strings.Trim(strings.TrimSpace(v), `"'`)
				case "description":
					description = strings.Trim(strings.TrimSpace(v), `"'`)
				}
			}
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			headings = append(headings, strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")))
		}
	}
	return name, description, headings
}

func (s *Store) CreateConversionDraft(id, title, target, sourcePath, sourceText string) (Conversion, error) {
	if err := checkID(id); err != nil {
		return Conversion{}, err
	}
	if target != "domain" && target != "workflow" {
		return Conversion{}, fmt.Errorf("conversion target must be domain or workflow, got %q", target)
	}
	if strings.TrimSpace(sourceText) == "" {
		return Conversion{}, fmt.Errorf("conversion needs the source skill text")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.lib.Conversions {
		if c.ID == id {
			return Conversion{}, fmt.Errorf("conversion %s already exists", id)
		}
	}
	_, _, headings := ParseSkillOutline(sourceText)
	c := Conversion{
		ID: id, Title: title, Target: target, SourcePath: sourcePath,
		SourceSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(sourceText))),
		SourceText:   sourceText, Headings: headings, Status: ConversionDraft,
		Revision: s.nextRevisionLocked(), CreatedAt: now(), UpdatedAt: now(),
	}
	s.lib.Conversions = append(s.lib.Conversions, c)
	if err := s.saveLocked(); err != nil {
		s.lib.Conversions = s.lib.Conversions[:len(s.lib.Conversions)-1]
		return Conversion{}, err
	}
	return c, nil
}

func (s *Store) conversionIndex(id string) int {
	for i := range s.lib.Conversions {
		if s.lib.Conversions[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) GetConversion(id string) (Conversion, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.conversionIndex(id); i >= 0 {
		return s.lib.Conversions[i], true
	}
	return Conversion{}, false
}

func (s *Store) ListConversions() []Conversion {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Conversion(nil), s.lib.Conversions...)
}

// UncoveredHeadings lists source headings with no mapped piece and no
// preserve entry: publish refuses while any remain, so no part of the
// source skill is silently dropped.
func (c Conversion) UncoveredHeadings(preserve []string) []string {
	covered := map[string]bool{}
	for _, p := range c.Pieces {
		covered[p.SourceHeading] = true
	}
	for _, h := range preserve {
		covered[h] = true
	}
	var out []string
	for _, h := range c.Headings {
		if !covered[h] {
			out = append(out, h)
		}
	}
	return out
}

func (s *Store) AddConversionPiece(id string, expectedRevision int64, piece ConversionPiece) (Conversion, error) {
	if piece.Kind != "section" && piece.Kind != "example" && piece.Kind != "negative" {
		return Conversion{}, fmt.Errorf("piece kind must be section, example, or negative, got %q", piece.Kind)
	}
	if strings.TrimSpace(piece.Title) == "" || strings.TrimSpace(piece.Body) == "" {
		return Conversion{}, fmt.Errorf("conversion piece needs a title and a body")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.conversionIndex(id)
	if i < 0 {
		return Conversion{}, fmt.Errorf("unknown conversion %s", id)
	}
	c := &s.lib.Conversions[i]
	if c.Status != ConversionDraft {
		return Conversion{}, fmt.Errorf("conversion %s is already published", id)
	}
	if expectedRevision != 0 && expectedRevision != c.Revision {
		return Conversion{}, fmt.Errorf("conversion %s revision is %d, not %d", id, c.Revision, expectedRevision)
	}
	known := piece.SourceHeading == ""
	for _, h := range c.Headings {
		if h == piece.SourceHeading {
			known = true
		}
	}
	if !known {
		return Conversion{}, fmt.Errorf("source_heading %q is not a heading of the source skill", piece.SourceHeading)
	}
	c.Pieces = append(c.Pieces, piece)
	c.Revision = s.nextRevisionLocked()
	c.UpdatedAt = now()
	if err := s.saveLocked(); err != nil {
		c.Pieces = c.Pieces[:len(c.Pieces)-1]
		return Conversion{}, err
	}
	return *c, nil
}

func (s *Store) MarkConversionPublished(id string, keys []string) (Conversion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.conversionIndex(id)
	if i < 0 {
		return Conversion{}, fmt.Errorf("unknown conversion %s", id)
	}
	c := &s.lib.Conversions[i]
	c.Status = ConversionPublished
	c.PublishedKeys = append([]string(nil), keys...)
	c.Revision = s.nextRevisionLocked()
	c.UpdatedAt = now()
	if err := s.saveLocked(); err != nil {
		return Conversion{}, err
	}
	return *c, nil
}
