// Package files provides bounded, hash-guarded workspace file operations.
package files

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"specific-harness/internal/pathutil"
	"strings"
	"sync"
	"unicode/utf8"

	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/search"
)

// Workspace owns no descriptor. Its mutex serializes harness file operations;
// expected hashes remain optimistic against an uncooperative external editor.
type Workspace struct {
	Path         string
	Caps         config.BashConfig
	Prompts      *prompts.Catalog
	mu           sync.Mutex
	publications map[string]uint64
	outside      bool // Read-only temporary root; displays absolute locators and grants no coverage.
}

func Capture(caps config.BashConfig, catalogs ...*prompts.Catalog) (*Workspace, error) {
	p, e := os.Getwd()
	if e != nil {
		return nil, fmt.Errorf("capture workspace: %w", e)
	}
	p, e = pathutil.EvalSymlinks(p)
	if e != nil {
		return nil, fmt.Errorf("capture workspace: %w", e)
	}
	return &Workspace{Path: p, Caps: caps, Prompts: prompts.Select(catalogs...)}, nil
}
func digest(b []byte) string  { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func validText(b []byte) bool { return utf8.Valid(b) && !strings.ContainsRune(string(b), 0) }
func (w *Workspace) bytes(ctx context.Context, r *os.Root, p string) ([]byte, os.FileInfo, error) {
	info, e := r.Lstat(p)
	if e != nil {
		return nil, nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a regular file (symlinks refused)", p)
	}
	f, e := r.Open(p)
	if e != nil {
		return nil, nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(sourceReader{ctx, f})
	if e != nil {
		return nil, nil, e
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !validText(b) {
		return nil, nil, fmt.Errorf("%s is binary or invalid UTF-8", p)
	}
	return b, info, nil
}

// Source snapshots are independent of returned-output caps. Reads remain cancellable;
// edit and Go parsing retain the actual complete source in memory.
type sourceReader struct {
	ctx context.Context
	io.Reader
}

func (r sourceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

// readWindow hashes and validates the entire stream, retaining only bounded complete lines.
func (w *Workspace) readWindow(ctx context.Context, r *os.Root, p string, off, limit int64) (string, []string, int64, error) {
	info, err := r.Lstat(p)
	if err != nil {
		return "", nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, 0, fmt.Errorf("%s is not a regular file (symlinks refused)", p)
	}
	f, err := r.Open(p)
	if err != nil {
		return "", nil, 0, err
	}
	defer f.Close()
	reader, hash := bufio.NewReader(f), sha256.New()
	var lines []string
	var lineBytes, incomplete []byte
	line, retained := int64(1), 0
	hasLine, full := false, false
	for {
		if err := ctx.Err(); err != nil {
			return "", nil, 0, err
		}
		part, err := reader.ReadSlice('\n')
		if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
			return "", nil, 0, err
		}
		hash.Write(part)
		// UTF8 may cross a reader fragment; carry only its incomplete final rune.
		text := append(incomplete, part...)
		incomplete = nil
		for len(text) > 0 {
			if !utf8.FullRune(text) {
				incomplete = append(incomplete, text...)
				break
			}
			c, n := utf8.DecodeRune(text)
			if c == 0 || c == utf8.RuneError && n == 1 {
				return "", nil, 0, fmt.Errorf("%s is binary or invalid UTF-8", p)
			}
			text = text[n:]
		}
		hasLine = hasLine || len(part) > 0
		if line >= off && int64(len(lines)) < limit && !full {
			if retained+len(part) > w.Caps.MaxBytes {
				lineBytes, full = nil, true // Never return or grant a partial source line.
			} else {
				lineBytes = append(lineBytes, part...)
				retained += len(part)
			}
		}
		if len(part) > 0 && part[len(part)-1] == '\n' {
			if len(lineBytes) > 0 {
				lines = append(lines, string(lineBytes))
			}
			lineBytes, hasLine = nil, false
			line++
		}
		if err == io.EOF {
			if len(incomplete) > 0 {
				return "", nil, 0, fmt.Errorf("%s is binary or invalid UTF-8", p)
			}
			total := line - 1
			if hasLine {
				total++
				if len(lineBytes) > 0 {
					lines = append(lines, string(lineBytes))
				}
			}
			return fmt.Sprintf("%x", hash.Sum(nil)), lines, total, nil
		}
	}
}

// Directory pages are live enumeration, not file bytes or observed source coverage.
func (w *Workspace) readDirectory(ctx context.Context, r *os.Root, p string, a tool.Args) (tool.Result, error) {
	off, _ := a["offset"].(int64)
	limit, _ := a["limit"].(int64)
	if off < 1 {
		return tool.Result{}, fmt.Errorf("offset=%d must be >=1", off)
	}
	if limit < 1 || limit > int64(w.Caps.MaxLines) {
		return tool.Result{}, fmt.Errorf("limit=%d must be within 1..%d (tools.bash.max_lines)", limit, w.Caps.MaxLines)
	}
	f, err := r.Open(p)
	if err != nil {
		return tool.Result{}, err
	}
	defer f.Close()
	display := w.displayPath(p)
	text := fmt.Sprintf("Directory %q (live directory page; names/order may shift; no file hash or source coverage)\n", display)
	pattern, _ := a["pattern"].(string)
	dirsOnly := pattern != ""
	if dirsOnly {
		quotedPath, _ := json.Marshal(display)
		readTool := "read"
		if w.outside {
			readTool = "read_outside"
		}
		quotedPattern, _ := json.Marshal(pattern)
		text = fmt.Sprintf("Directory %q (immediate real subdirectories matching %s; files excluded; %s(path=%s) lists all children; live page, names/order may shift; no file hash or source coverage)\n", display, quotedPattern, readTool, quotedPath)
	}
	shown, more := int64(0), false
	footer := func(count int64, more bool) string {
		cursor := ""
		if more {
			cursor = fmt.Sprintf("; next_offset=%d", off+count)
		}
		return fmt.Sprintf("[%d entries shown from entry %d; EOF=%t; truncated=%t%s]", count, off, !more, more, cursor)
	}
	for i := int64(0); ; {
		if err = ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		entries, e := f.ReadDir(1)
		if e == io.EOF {
			break
		}
		if e != nil {
			return tool.Result{}, e
		}
		entry := entries[0]
		if dirsOnly {
			matched, err := filepath.Match(pattern, entry.Name())
			if err != nil {
				return tool.Result{}, err
			}
			if !entry.IsDir() || pathutil.IsLinkMode(entry.Type()) || !matched {
				continue
			}
		}
		i++
		if i < off {
			continue
		}
		if shown == limit {
			more = true
			break
		}
		path := filepath.Join(display, entry.Name())
		if !validText([]byte(path)) {
			return tool.Result{}, fmt.Errorf("directory contains a non-UTF8 path")
		}
		kind := "other"
		switch {
		case pathutil.IsLinkMode(entry.Type()):
			kind = "symlink"
		case entry.IsDir():
			kind = "dir"
		case entry.Type().IsRegular():
			kind = "file"
		}
		quoted, _ := json.Marshal(path)
		row := kind + " " + string(quoted) + "\n"
		if len(text)+len(row)+len(footer(shown+1, true)) > w.Caps.MaxBytes {
			if shown == 0 {
				return tool.Result{}, fmt.Errorf("directory entry and metadata exceed tools.bash.max_bytes=%d; %s", w.Caps.MaxBytes, prompts.Select(w.Prompts).Text("guidance.files.directory_page"))
			}
			more = true
			break
		}
		text += row
		shown++
	}
	tail := footer(shown, more)
	if len(text)+len(tail) > w.Caps.MaxBytes {
		return tool.Result{}, fmt.Errorf("directory metadata exceeds tools.bash.max_bytes=%d", w.Caps.MaxBytes)
	}
	return tool.Result{Text: text + tail, Truncated: more, NoMatches: shown == 0}, nil
}

// FindDirDefinition lists immediate real directories, including hidden/ignored/empty ones.
func (w *Workspace) FindDirDefinition() tool.Definition {
	pc := prompts.Select(w.Prompts)
	return w.normalizePath(tool.Definition{Prompts: pc, Name: "find_dir", Desc: pc.Text("tools.find_dir.description"), Purpose: pc.Format("tools.find_dir.purpose", w.Caps.MaxLines, w.Caps.MaxBytes), Params: []tool.Param{
		{Name: "path", Type: tool.TypeString, Default: ".", Desc: pc.Text("tools.find_dir.parameters.path")},
		{Name: "pattern", Type: tool.TypeString, Default: "*", Desc: pc.Text("tools.find_dir.parameters.pattern")},
		{Name: "offset", Type: tool.TypeInt, Default: "1", Desc: pc.Text("tools.find_dir.parameters.offset")},
		{Name: "limit", Type: tool.TypeInt, Default: fmt.Sprint(w.Caps.MaxLines), Desc: pc.Text("tools.find_dir.parameters.limit")},
	}, Execute: func(ctx context.Context, a tool.Args) (tool.Result, error) {
		pattern, _ := a["pattern"].(string)
		if pattern == "" || strings.ContainsAny(pattern, `/\`) {
			return tool.Result{}, fmt.Errorf("pattern must be one nonempty directory basename glob")
		}
		if _, err := filepath.Match(pattern, ""); err != nil {
			return tool.Result{}, err
		}
		return w.execute(ctx, "find_dir", a, nil)
	}})
}

type readCoverage struct {
	hash    string
	version uint64
	ranges  [][2]int64
}

func (w *Workspace) Definitions() []tool.Definition {
	pc := prompts.Select(w.Prompts)
	// Authority belongs to this conversation's definitions, not sibling registries.
	coverage := map[string]readCoverage{}
	path := tool.Param{Name: "path", Type: tool.TypeString, Required: true, Desc: pc.Text("tools.shared.parameters.workspace_file_path")}
	defs := []tool.Definition{
		{Name: "read", Desc: pc.Text("tools.read.description"), Purpose: pc.Text("tools.read.purpose"), Params: []tool.Param{path, {Name: "offset", Type: tool.TypeInt, Default: "1", Desc: pc.Text("tools.read.parameters.offset")}, {Name: "limit", Type: tool.TypeInt, Default: fmt.Sprint(w.Caps.MaxLines), Desc: pc.Format("tools.read.parameters.limit", w.Caps.MaxLines)}}},
		{Name: "edit", Desc: pc.Text("tools.edit.description"), Purpose: pc.Text("tools.edit.purpose"), Params: []tool.Param{path, {Name: "old", Type: tool.TypeString, Desc: pc.Text("tools.edit.parameters.old")}, {Name: "start_line", Type: tool.TypeInt, Desc: pc.Text("tools.edit.parameters.start_line")}, {Name: "end_line", Type: tool.TypeInt, Desc: pc.Text("tools.edit.parameters.end_line")}, {Name: "new", Type: tool.TypeString, Required: true, AllowEmpty: true, Desc: pc.Text("tools.edit.parameters.new")}, {Name: "expected_hash", Type: tool.TypeString, Desc: pc.Text("tools.edit.parameters.expected_hash")}}},
		{Name: "write", Desc: pc.Text("tools.write.description"), Purpose: pc.Text("tools.write.purpose"), Params: []tool.Param{path, {Name: "content", Type: tool.TypeString, Required: true, AllowEmpty: true, Desc: pc.Text("tools.write.parameters.content")}, {Name: "expected_hash", Type: tool.TypeString, Desc: pc.Text("tools.write.parameters.expected_hash")}}},
	}
	defs[0].Params[0].Desc = pc.Text("tools.read.parameters.path")
	defs[0].Params = append(defs[0].Params, w.navigationParams()...)
	for i := range defs {
		defs[i].Prompts = pc
		name := defs[i].Name
		defs[i].Execute = func(ctx context.Context, a tool.Args) (tool.Result, error) { return w.execute(ctx, name, a, coverage) }
		defs[i] = w.normalizePath(defs[i])
	}
	return defs
}

func (w *Workspace) normalizePath(def tool.Definition) tool.Definition {
	def.NormalizeArgs = func(ctx context.Context, args tool.Args) (tool.Args, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		input, _ := args["path"].(string)
		decoded, err := search.ExistingPath(w.Path, input)
		if err != nil {
			return nil, err
		}
		path, _, err := w.filePath(decoded, def.Name == "read" || def.Name == "find_dir")
		if err != nil {
			return nil, err
		}
		normalized := make(tool.Args, len(args))
		for key, value := range args {
			normalized[key] = value
		}
		normalized["path"] = path
		return normalized, nil
	}
	return def
}

// The execution owner repeats physical scope admission without another decode.
func (w *Workspace) filePath(p string, discovery bool) (string, string, error) {
	if p == "" || (!filepath.IsAbs(p) && (filepath.Clean(p) == ".." || strings.HasPrefix(filepath.Clean(p), ".."+string(filepath.Separator)))) {
		return "", "", fmt.Errorf("path must stay within workspace %s", w.Path)
	}
	p = filepath.Clean(p)
	base, e := filepath.Abs(w.Path)
	if e != nil {
		return "", "", e
	}
	base, e = pathutil.EvalSymlinks(base)
	if e != nil {
		return "", "", e
	}
	parentPath := filepath.Dir(p)
	if !filepath.IsAbs(p) {
		parentPath = filepath.Join(base, parentPath)
	}
	parent, e := pathutil.EvalSymlinks(parentPath)
	if e != nil {
		return "", "", e
	}
	if discovery && filepath.Join(parent, filepath.Base(p)) == base {
		p = "."
	} else {
		parent, e = filepath.Rel(base, parent)
		if e != nil || parent == ".." || strings.HasPrefix(parent, ".."+string(filepath.Separator)) {
			return "", "", fmt.Errorf("path must stay within workspace %s", w.Path)
		}
		p = filepath.Join(parent, filepath.Base(p))
	}
	return p, base, nil
}
func (w *Workspace) execute(ctx context.Context, name string, a tool.Args, coverage map[string]readCoverage) (tool.Result, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return tool.Result{}, e
	}
	p, _ := a["path"].(string)
	p, base, e := w.filePath(p, name == "read" || name == "find_dir")
	if e != nil {
		return tool.Result{}, e
	}
	r, e := os.OpenRoot(base)
	if e != nil {
		return tool.Result{}, e
	}
	defer r.Close()
	if name == "read" || name == "find_dir" {
		info, err := r.Lstat(p)
		if err == nil && pathutil.IsLinkMode(info.Mode()) {
			return tool.Result{}, fmt.Errorf("%s is a final symlink or reparse point; supply its actual path", p)
		}
		if err == nil && info.IsDir() {
			if navigationRequested(a) {
				return tool.Result{}, fmt.Errorf("definition and outline require a file, not a directory")
			}
			return w.readDirectory(ctx, r, p, a)
		}
		if name == "find_dir" {
			return tool.Result{}, fmt.Errorf("%s is not a real directory (symlinks refused): %v", p, err)
		}
	}
	if name == "read" && navigationRequested(a) {
		return w.readNavigation(ctx, r, p, a, coverage)
	}
	var b []byte
	var info os.FileInfo
	var lines []string
	var hash string
	var total, off, limit int64
	if name == "read" {
		off, _ = a["offset"].(int64)
		limit, _ = a["limit"].(int64)
		if off < 1 {
			return tool.Result{}, fmt.Errorf("offset=%d must be >=1", off)
		}
		if limit < 1 || limit > int64(w.Caps.MaxLines) {
			return tool.Result{}, fmt.Errorf("limit=%d must be within 1..%d (tools.bash.max_lines)", limit, w.Caps.MaxLines)
		}
		hash, lines, total, e = w.readWindow(ctx, r, p, off, limit)
	} else {
		b, info, e = w.bytes(ctx, r, p)
		hash = digest(b)
	}
	issued, issuedOK := coverage[p]
	if issuedOK && (e != nil || issued.hash != hash || issued.version != w.publications[p]) {
		delete(coverage, p)
	}
	expected, _ := a["expected_hash"].(string)
	create := name == "write" && expected == ""
	if create {
		if e == nil {
			return tool.Result{}, fmt.Errorf("%s exists; %s", p, prompts.Select(w.Prompts).Text("guidance.files.existing_create"))
		}
		if !os.IsNotExist(e) {
			return tool.Result{}, e
		}
	} else if e != nil {
		return tool.Result{}, e
	}
	if name == "read" {
		text := fmt.Sprintf("%s sha256=%s\n", w.displayPath(p), hash)
		footer := func(shown int64, more bool) string {
			span, cursor := "none", ""
			if shown > 0 {
				span = fmt.Sprintf("%d-%d", off, off+shown-1)
				if more {
					cursor = fmt.Sprintf("; next_offset=%d", off+shown)
				}
			}
			return fmt.Sprintf("\n[%d lines shown from line %d; shown_range=%s; EOF=%t; truncated=%t%s; metadata not source lines]", shown, off, span, !more, more, cursor)
		}
		truncated := false
		shown := int64(0)
		for i, line := range lines {
			if len(text)+len(line)+len(footer(shown+1, off+int64(i) < total)) > w.Caps.MaxBytes {
				truncated = true
				break
			}
			text += line
			shown++
		}
		if off-1+shown < total {
			truncated = true
		}
		tail := footer(shown, truncated)
		if len(text)+len(tail) > w.Caps.MaxBytes {
			return tool.Result{}, fmt.Errorf("read metadata exceeds tools.bash.max_bytes=%d", w.Caps.MaxBytes)
		}
		if err := ctx.Err(); err != nil {
			return tool.Result{}, err
		}
		if shown > 0 && coverage != nil {
			w.recordRead(coverage, p, hash, off, off+shown-1)
		}
		return tool.Result{Text: text + tail, Truncated: truncated, NoMatches: shown == 0 && total > 0}, nil
	}
	// An exact-span edit whose old text matches uniquely (enforced below)
	// does not need expected_hash: uniqueness already pins the base content.
	hashOptional := name == "edit" && expected == ""
	if !create && !hashOptional && expected != digest(b) {
		// Discriminate a mistyped hash (file unchanged since the issued
		// read) from a genuinely changed file: the first must not send
		// the model back to reread content it already has.
		if issuedOK && issued.hash == digest(b) && expected != issued.hash {
			return tool.Result{}, fmt.Errorf("%s: %s", p, prompts.Select(w.Prompts).Format("guidance.files.mistyped_hash", digest(b)))
		}
		return tool.Result{}, fmt.Errorf("%s changed; %s", p, prompts.Select(w.Prompts).Text("guidance.files.changed_hash"))
	}
	content, _ := a["content"].(string)
	deleted, inserted, scope := string(b), content, "whole-file replacement"
	if create {
		scope = "whole-file creation"
	}
	if name == "edit" {
		old, _ := a["old"].(string)
		replacement, _ := a["new"].(string)
		first, hasFirst := a["start_line"]
		last, hasLast := a["end_line"]
		if hasFirst || hasLast {
			start, startOK := first.(int64)
			end, endOK := last.(int64)
			lines := sourceLines(string(b))
			if old != "" {
				return tool.Result{}, fmt.Errorf("edit mixes exact-span and line-range routes; %s", prompts.Select(w.Prompts).Text("guidance.files.mixed_edit_routes"))
			}
			if !hasFirst || !hasLast || !startOK || !endOK {
				return tool.Result{}, fmt.Errorf("line-range edit needs both integer start_line and end_line; %s", prompts.Select(w.Prompts).Text("guidance.files.incomplete_range"))
			}
			if expected == "" {
				return tool.Result{}, fmt.Errorf("line-range edit needs expected_hash from the current read; %s", prompts.Select(w.Prompts).Text("guidance.files.changed_hash"))
			}
			if start < 1 || end < start || end > int64(len(lines)) {
				return tool.Result{}, fmt.Errorf("range %d-%d outside source lines 1-%d; read metadata is not source and does not count toward line numbering", start, end, len(lines))
			}
			observed := false
			for _, span := range coverage[p].ranges {
				observed = observed || span[0] <= start && span[1] >= end
			}
			if !observed {
				return tool.Result{}, fmt.Errorf("lines %d-%d not returned by current read; %s", start, end, prompts.Select(w.Prompts).Format("guidance.files.unobserved_range", p, start, min(end-start+1, int64(w.Caps.MaxLines)), end))
			}
			if replacement != "" && end < int64(len(lines)) && !strings.HasSuffix(replacement, "\n") {
				return tool.Result{}, fmt.Errorf("line-range replacement would join untouched line %d; end new with a line separator (LF or CRLF), or use an exact old span for an intentional join; file unchanged", end+1)
			}
			deleted, inserted, scope = strings.Join(lines[int(start-1):int(end)], ""), replacement, fmt.Sprintf("inclusive lines %d-%d", start, end)
			content = strings.Join(lines[:int(start-1)], "") + replacement + strings.Join(lines[int(end):], "")
		} else {
			if old == "" {
				return tool.Result{}, fmt.Errorf("old is empty; %s", prompts.Select(w.Prompts).Text("guidance.files.empty_old"))
			}
			if n := strings.Count(string(b), old); n != 1 {
				remedy := prompts.Select(w.Prompts).Text("guidance.files.missing_old")
				if n > 1 {
					remedy = prompts.Select(w.Prompts).Text("guidance.files.ambiguous_old")
				}
				return tool.Result{}, fmt.Errorf("old matched %d times; %s", n, remedy)
			}
			deleted, inserted, scope = old, replacement, "exact-span"
			content = strings.Replace(string(b), old, replacement, 1)
		}
	}
	next := []byte(content)
	if !validText(next) {
		return tool.Result{}, fmt.Errorf("content must be UTF8/nonbinary")
	}
	if !create && string(b) == content {
		return tool.Result{Text: fmt.Sprintf("%s unchanged sha256=%s", p, expected)}, nil
	}
	receipt, truncated, e := publicationReceipt(p, scope, b, next, deleted, inserted, w.Caps.MaxBytes)
	if e != nil {
		return tool.Result{}, e
	}
	res, e := w.publish(ctx, r, p, b, next, info, create)
	if res.Changed {
		if w.publications == nil {
			w.publications = map[string]uint64{}
		}
		w.publications[p]++
		published, identityErr := r.Lstat(p)
		for path := range w.publications {
			if path == p {
				continue
			}
			current, statErr := r.Lstat(path)
			// Compare entries after rename: hardlink siblings still own their old inode.
			if identityErr != nil || statErr != nil || os.SameFile(published, current) {
				w.publications[path]++
			}
		}
		delete(coverage, p)
		res.Text, res.Truncated = receipt, truncated
	}
	return res, e
}

// recordRead grants only complete source lines actually delivered by this registry.
func (w *Workspace) recordRead(coverage map[string]readCoverage, p, hash string, start, end int64) {
	if coverage == nil || end < start {
		return
	}
	if w.publications == nil {
		w.publications = map[string]uint64{}
	}
	version := w.publications[p]
	w.publications[p] = version
	c := coverage[p]
	if c.hash != hash || c.version != version {
		c.ranges = nil
	}
	c.hash, c.version = hash, version
	c.ranges = append(c.ranges, [2]int64{start, end})
	sort.Slice(c.ranges, func(i, j int) bool { return c.ranges[i][0] < c.ranges[j][0] })
	merged := c.ranges[:0]
	for _, span := range c.ranges {
		if len(merged) > 0 && span[0] <= merged[len(merged)-1][1]+1 {
			if span[1] > merged[len(merged)-1][1] {
				merged[len(merged)-1][1] = span[1]
			}
		} else {
			merged = append(merged, span)
		}
	}
	c.ranges = merged
	coverage[p] = c
}
func sourceLines(s string) []string {
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// Construct the bounded factual receipt before effects; source bytes are not repaired.
func publicationReceipt(path, scope string, b, next []byte, deleted, inserted string, cap int) (string, bool, error) {
	text := fmt.Sprintf("%q changed %s (%d→%d file lines) size_bytes=%d old_sha256=%s sha256=%s replacement_final_LF=%t file_final_LF=%t\n", path, scope, len(sourceLines(string(b))), len(sourceLines(string(next))), len(next), digest(b), digest(next), strings.HasSuffix(inserted, "\n"), strings.HasSuffix(string(next), "\n"))
	omission := "[preview omitted]"
	if len(text)+len(omission) > cap {
		return "", false, fmt.Errorf("publication receipt exceeds tools.bash.max_bytes=%d; no bytes published", cap)
	}
	sides := [][]string{sourceLines(deleted), sourceLines(inserted)}
	used := [2]int{}
	count := 0
	for count < 16 && (used[0] < len(sides[0]) || used[1] < len(sides[1])) {
		side := count % 2
		if used[side] >= len(sides[side]) {
			side = 1 - side
		}
		line := []string{"- ", "+ "}[side] + sides[side][used[side]]
		if !strings.HasSuffix(line, "\n") {
			line += "\n\\ No final LF\n"
		}
		room := cap - len(text) - len(omission)
		if len(line) > room {
			prefix := line[:room]
			for !utf8.ValidString(prefix) {
				prefix = prefix[:len(prefix)-1]
			}
			return text + prefix + omission, true, nil
		}
		text += line
		used[side]++
		count++
	}
	if used[0] < len(sides[0]) || used[1] < len(sides[1]) {
		text += omission
	}
	return text, used[0] < len(sides[0]) || used[1] < len(sides[1]), nil
}

func (w *Workspace) publish(ctx context.Context, r *os.Root, p string, b, next []byte, info os.FileInfo, create bool) (tool.Result, error) {
	nonce := make([]byte, 12)
	if _, e := rand.Read(nonce); e != nil {
		return tool.Result{}, e
	}
	tmp := filepath.Join(filepath.Dir(p), ".specific-"+hex.EncodeToString(nonce))
	perm := os.FileMode(0644)
	if info != nil {
		perm = info.Mode().Perm()
	}
	f, e := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if e != nil {
		return tool.Result{}, e
	}
	defer r.Remove(tmp)
	if info != nil {
		e = f.Chmod(perm)
	}
	if e == nil {
		_, e = f.Write(next)
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return tool.Result{}, e
	}
	if !create {
		current, now, e := w.bytes(ctx, r, p)
		if e != nil {
			return tool.Result{}, e
		}
		if !os.SameFile(info, now) || digest(current) != digest(b) {
			return tool.Result{}, fmt.Errorf("%s changed before publication; %s", p, prompts.Select(w.Prompts).Text("guidance.files.changed_publication"))
		}
	}
	if e = ctx.Err(); e != nil {
		return tool.Result{}, e
	}
	if create {
		e = r.Link(tmp, p)
	} else {
		e = r.Rename(tmp, p)
	}
	if e != nil {
		return tool.Result{}, e
	}
	return tool.Result{Changed: true, FileEffect: &tool.FileEffect{Path: filepath.Clean(p), BeforeHash: digest(b), AfterHash: digest(next), Created: create}, Text: fmt.Sprintf("%s changed old_sha256=%s sha256=%s (optimistic hash guard)", p, digest(b), digest(next))}, nil
}
