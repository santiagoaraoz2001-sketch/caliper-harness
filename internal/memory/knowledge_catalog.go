package memory

import (
	"embed"
	"io/fs"
	"regexp"
	"strings"
)

// The content is the catalog: links beside a section explain its useful next
// hops. Nothing here inserts guide bodies into automatic project recall.
//
//go:embed knowledge/*/*.md
var guides embed.FS

var builtinBodies, BuiltinKeys = loadBuiltinCatalog()

func loadBuiltinCatalog() (map[string]string, []string) {
	bodies := map[string]string{UIStandardKey: UIStandard, CodeStandardKey: CodeStandard, TestsStandardKey: TestsStandard}
	keys := []string{UIStandardKey, CodeStandardKey, TestsStandardKey}
	paths, _ := fs.Glob(guides, "knowledge/*/*.md")
	for _, path := range paths {
		body, _ := guides.ReadFile(path) // Paths come from the same embedded FS.
		key := "caliper/guides/" + strings.TrimSuffix(strings.TrimPrefix(path, "knowledge/"), ".md")
		bodies[key] = string(body)
		keys = append(keys, key)
	}
	return bodies, keys
}

var guideLink = regexp.MustCompile(`\[([^\]\n]+)\]\(knowledge:([a-zA-Z0-9/_-]+)(?:#([a-zA-Z0-9_-]+))?\)`)

// BuiltinReferences exposes purpose-labelled edges, without fetching target
// bodies. Status resolution inspects only the bundled target/anchor requested.
func BuiltinReferences(key string, resolveStatus bool) []Reference {
	refs := []Reference{}
	for _, section := range Sections(builtinBodies[key], "", "") {
		fence := ""
		for _, line := range strings.Split(section.Body, "\n") {
			var marker bool
			fence, marker = markdownFence(line, fence)
			if marker {
				continue
			}
			if fence != "" {
				continue
			}
			for _, match := range guideLink.FindAllStringSubmatch(line, -1) {
				ref := Reference{Key: match[2], Relation: "explains", Section: match[3], SourceSection: section.ID, Label: match[1]}
				if resolveStatus {
					ref.Status = "resolved"
					if body, ok := builtinBodies[ref.Key]; !ok {
						ref.Status = "missing"
					} else if ref.Section != "" && len(Sections(body, "", ref.Section)) == 0 {
						ref.Status = "missing-section"
					}
				}
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func SectionReferences(refs []Reference, anchor string) []Reference {
	out := []Reference{}
	for _, ref := range refs {
		if anchor == "" || ref.SourceSection == "" || ref.SourceSection == anchor {
			out = append(out, ref)
		}
	}
	return out
}
