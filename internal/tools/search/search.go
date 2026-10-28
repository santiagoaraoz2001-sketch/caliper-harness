// Package search implements the grep and find tools as Pi shapes them:
// ripgrep-backed, .gitignore-respecting, with match/byte/line caps.
// A missing rg binary is a loud error naming the install fix.
package search

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"specific-harness/internal/bundle"
	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
	"specific-harness/internal/tools/process"
)

// NewGrep returns the grep definition bound to cfg caps.
func NewGrep(cfg config.SearchConfig, catalogs ...*prompts.Catalog) tool.Definition {
	return NewGrepAt(cfg, "", catalogs...)
}
func NewGrepAt(cfg config.SearchConfig, dir string, catalogs ...*prompts.Catalog) tool.Definition {
	pc := prompts.Select(catalogs...)
	return normalizeSearchPath(dir, tool.Definition{Prompts: pc,
		Name:    "grep",
		Label:   "grep",
		Desc:    pc.Text("tools.grep.description"),
		Purpose: discoveryPurpose(pc, "tools.grep.purpose", dir),
		Params: []tool.Param{
			{Name: "pattern", Desc: pc.Text("tools.grep.parameters.pattern"), Type: tool.TypeString, Required: true},
			{Name: "path", Desc: pc.Text("tools.grep.parameters.path"), Type: tool.TypeString, Default: "."},
			{Name: "glob", Desc: pc.Text("tools.grep.parameters.glob"), Type: tool.TypeString},
			{Name: "ignore_case", Desc: pc.Text("tools.grep.parameters.ignore_case"), Type: tool.TypeBool, Default: "false"},
			{Name: "literal", Desc: pc.Text("tools.grep.parameters.literal"), Type: tool.TypeBool, Default: "false"},
			{Name: "context", Desc: pc.Text("tools.grep.parameters.context"), Type: tool.TypeInt, Default: "0"},
			{Name: "offset", Desc: pc.Text("tools.grep.parameters.offset"), Type: tool.TypeInt, Default: "0"},
			{Name: "limit", Desc: pc.Text("tools.grep.parameters.limit"), Type: tool.TypeInt, Default: fmt.Sprint(cfg.MaxMatches)},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err
			}
			args, err := scopedArgs(dir, args)
			if err != nil {
				return tool.Result{}, err
			}
			return runGrep(ctx, cfg, args, dir, pc)
		},
	})
}

// NewFind returns the find definition: glob file listing via ripgrep.
func NewFind(cfg config.SearchConfig, catalogs ...*prompts.Catalog) tool.Definition {
	return NewFindAt(cfg, "", catalogs...)
}
func NewFindAt(cfg config.SearchConfig, dir string, catalogs ...*prompts.Catalog) tool.Definition {
	pc := prompts.Select(catalogs...)
	return normalizeSearchPath(dir, tool.Definition{Prompts: pc,
		Name:    "find",
		Label:   "find",
		Desc:    pc.Text("tools.find.description"),
		Purpose: discoveryPurpose(pc, "tools.find.purpose", dir),
		Params: []tool.Param{
			{Name: "pattern", Desc: pc.Text("tools.find.parameters.pattern"), Type: tool.TypeString, Required: true},
			{Name: "path", Desc: pc.Text("tools.find.parameters.path"), Type: tool.TypeString, Default: "."},
			{Name: "offset", Desc: pc.Text("tools.find.parameters.offset"), Type: tool.TypeInt, Default: "0"},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err
			}
			args, err := scopedArgs(dir, args)
			if err != nil {
				return tool.Result{}, err
			}
			return runFind(ctx, cfg, args, dir, pc)
		},
	})
}

// NewFindFileAt retains the recursive file finder; find remains a compatibility definition.
func NewFindFileAt(cfg config.SearchConfig, dir string, catalogs ...*prompts.Catalog) tool.Definition {
	d := NewFindAt(cfg, dir, catalogs...)
	d.Name, d.Label = "find_file", "find_file"
	return d
}

func rgBinary(catalogs ...*prompts.Catalog) (string, error) {
	pc := prompts.Select(catalogs...)
	p, err := bundle.Tool("rg")
	if err != nil {
		if bundle.Root() != "" {
			return "", err
		}
		return "", fmt.Errorf("ripgrep (rg) is not installed — search needs it: %s", pc.Text("guidance.search.missing_rg"))
	}
	return p, nil
}

func grepZero(cfg config.SearchConfig, catalogs ...*prompts.Catalog) tool.Result {
	pc := prompts.Select(catalogs...)
	text := pc.Text("discovery.grep.no_matches_guidance")
	return tool.Result{Text: text[:min(len(text), max(0, cfg.MaxBytes))], Truncated: len(text) > cfg.MaxBytes, NoMatches: true}
}

func findZero(cfg config.SearchConfig, catalogs ...*prompts.Catalog) tool.Result {
	pc := prompts.Select(catalogs...)
	text := pc.Text("discovery.find.no_matches_guidance")
	return tool.Result{Text: text[:min(len(text), max(0, cfg.MaxBytes))], Truncated: len(text) > cfg.MaxBytes, NoMatches: true}
}

func runGrep(ctx context.Context, cfg config.SearchConfig, args tool.Args, dir string, catalogs ...*prompts.Catalog) (tool.Result, error) {
	pc := prompts.Select(catalogs...)
	rg, err := rgBinary(pc)
	if err != nil {
		return tool.Result{}, err
	}
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)
	glob, _ := args["glob"].(string)
	ignoreCase, _ := args["ignore_case"].(bool)
	literal, _ := args["literal"].(bool)
	context, _ := args["context"].(int64)
	limit, _ := args["limit"].(int64)
	if path == "" {
		path = "."
	}
	if limit <= 0 || limit > int64(cfg.MaxMatches) {
		return tool.Result{}, fmt.Errorf("limit=%d must be within 1..%d (tools.search.max_matches)", limit, cfg.MaxMatches)
	}
	offset, err := pageOffset(args, limit)
	if err != nil {
		return tool.Result{}, err
	}
	if glob != "" && !doublestar.ValidatePattern(glob) {
		return tool.Result{}, fmt.Errorf("invalid grep glob %q", glob)
	}
	// NOTE: no --glob is passed to rg. An explicit rg glob whitelists
	// matches past .gitignore; filtering happens in Go below so ignores win.
	// Include skipped matches plus one actual overflow record per file.
	rgArgs := []string{"--json", "--sort", "path", "--with-filename", "--line-number", "--color", "never", "--max-count", strconv.FormatInt(offset+limit+1, 10)}
	if ignoreCase {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if literal {
		rgArgs = append(rgArgs, "--fixed-strings")
	}
	if context > 0 {
		rgArgs = append(rgArgs, "--context", strconv.FormatInt(context, 10))
	}
	if dir != "" {
		rgArgs = append(rgArgs, "--no-follow")
	}
	rgArgs = append(rgArgs, "--", pattern, path)
	cmd := process.Command(ctx, rg, rgArgs...)
	cmd.Dir = dir
	out, stderr, overflow, err := captureSearch(ctx, cmd, cfg.MaxCaptureBytes, pc)
	if overflow || ctx.Err() != nil {
		if overflow {
			out = nil
		} // Unparseable stdout is not a model-visible page.
		return tool.Result{Text: rawPrefix(out, stderr, cfg.MaxBytes), Failed: true, Truncated: overflow}, errors.Join(err, ctx.Err())
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			if exit.ExitCode() == 1 {
				return grepZero(cfg, pc), nil
			}
			return tool.Result{}, fmt.Errorf("search failed: %s: %w", strings.TrimSpace(string(stderr[:min(len(stderr), cfg.MaxBytes)])), err)
		}
		return tool.Result{}, fmt.Errorf("cannot run ripgrep: %w", err)
	}
	var lines, pending []searchLine
	var seen int64
	matches, lastLine := 0, 0
	lastPath, admitted, truncated := "", false, false
	decoder := json.NewDecoder(bytes.NewReader(out))
	for {
		var record struct {
			Type string
			Data struct {
				Path, Lines rgText
				LineNumber  int `json:"line_number"`
			}
		}
		if err := decoder.Decode(&record); err == io.EOF {
			break
		} else if err != nil {
			return tool.Result{}, fmt.Errorf("invalid ripgrep JSON: %w", err)
		}
		if record.Type == "begin" {
			pending, admitted, lastPath, lastLine = nil, false, "", 0
			if context > 0 && len(lines) > 0 {
				pending = append(pending, searchLine{prefix: "--"})
			}
		}
		if record.Type != "match" && record.Type != "context" {
			continue
		}
		file, err := record.Data.Path.value()
		if err != nil {
			return tool.Result{}, fmt.Errorf("ripgrep filename: %w", err)
		}
		if glob != "" && !globMatchFile(glob, file, path, dir) {
			continue
		}
		if dir != "" {
			file = searchLocator(file)
		}
		body, err := record.Data.Lines.value()
		if err != nil {
			return tool.Result{}, fmt.Errorf("ripgrep line: %w", err)
		}
		separator := "-"
		if record.Type == "match" {
			if seen < offset {
				seen++
				pending, admitted = nil, false
				lastPath, lastLine = file, record.Data.LineNumber
				continue
			}
			if matches == int(limit) {
				truncated = true
				break
			}
			matches++
			separator, admitted = ":", true
			lines, pending = append(lines, pending...), nil
		}
		if lastPath != "" && (lastPath != file || record.Data.LineNumber > lastLine+1) {
			if admitted {
				lines = append(lines, searchLine{prefix: "--"})
			} else {
				pending = append(pending, searchLine{prefix: "--"})
			}
		}
		line := searchLine{prefix: fmt.Sprintf("%s%s%d%s", file, separator, record.Data.LineNumber, separator), body: strings.TrimSuffix(body, "\n"), match: record.Type == "match", path: file, number: record.Data.LineNumber, lf: strings.HasSuffix(body, "\n")}
		if admitted {
			lines = append(lines, line)
		} else {
			pending = append(pending, line)
		}
		lastPath, lastLine = file, record.Data.LineNumber
	}
	cfg.MaxMatches = int(limit)
	if len(lines) == 0 {
		return grepZero(cfg, pc), nil
	}
	return pageText(lines, cfg, offset, truncated, pc)
}

// rg encodes non-UTF8 paths/lines in bytes; refuse loudly rather than let
// later JSON encoding silently replace the source identity/content.
type rgText struct {
	Text  *string
	Bytes string
}

func (s rgText) value() (string, error) {
	if s.Text != nil {
		return *s.Text, nil
	}
	b, err := base64.StdEncoding.DecodeString(s.Bytes)
	if err != nil {
		return "", fmt.Errorf("invalid base64 bytes: %w", err)
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("non-UTF8 bytes (base64 %q) cannot be rendered safely", s.Bytes)
	}
	return string(b), nil
}

// globMatchFile matches a path against a glob. Patterns without a slash
// match the basename (rg semantics); others match the full relative path.
func globMatchFile(pattern, p, root, dir string) bool {
	pattern, p = filepath.ToSlash(pattern), filepath.ToSlash(p)
	hasPath := strings.Contains(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "./")
	if hasPath && pattern == strings.TrimPrefix(p, "./") {
		return true
	}
	if hasPath && dir != "" {
		names := [2]string{pattern, strings.TrimPrefix(p, "./")}
		for i, name := range names {
			if filepath.IsAbs(name) {
				if relative, err := filepath.Rel(dir, name); err == nil {
					names[i] = filepath.ToSlash(relative)
				}
			}
		}
		if names[0] == names[1] {
			return true
		}
	}
	if hasPath {
		if relative, err := filepath.Rel(root, p); err == nil && relative != "." {
			p = filepath.ToSlash(relative)
		}
	}
	p = strings.TrimPrefix(p, "./")
	name := p
	if !hasPath {
		name = path.Base(p)
	}
	ok, err := doublestar.Match(pattern, name)
	return err == nil && ok
}

func runFind(ctx context.Context, cfg config.SearchConfig, args tool.Args, dir string, catalogs ...*prompts.Catalog) (tool.Result, error) {
	pc := prompts.Select(catalogs...)
	rg, err := rgBinary(pc)
	if err != nil {
		return tool.Result{}, err
	}
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)
	if path == "" {
		path = "."
	}
	offset, err := pageOffset(args, int64(cfg.MaxMatches))
	if err != nil {
		return tool.Result{}, err
	}
	if !doublestar.ValidatePattern(pattern) {
		return tool.Result{}, fmt.Errorf("invalid find glob %q", pattern)
	}
	rgArgs := []string{"--files", "--sort", "path"}
	if dir != "" {
		rgArgs = append(rgArgs, "--no-follow", "--")
	}
	rgArgs = append(rgArgs, path)
	cmd := process.Command(ctx, rg, rgArgs...)
	cmd.Dir = dir
	out, stderr, overflow, err := captureSearch(ctx, cmd, cfg.MaxCaptureBytes, pc)
	if overflow || ctx.Err() != nil {
		if overflow {
			out = nil
		} // Unparseable stdout is not a model-visible page.
		return tool.Result{Failed: true, Truncated: overflow, Text: rawPrefix(out, stderr, cfg.MaxBytes)}, errors.Join(err, ctx.Err())
	}
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return findZero(cfg, pc), nil
		}
		return tool.Result{}, fmt.Errorf("find failed: %s: %w", strings.TrimSpace(string(stderr[:min(len(stderr), cfg.MaxBytes)])), err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if !utf8.ValidString(line) {
			return tool.Result{}, fmt.Errorf("find filename has non-UTF8 bytes (base64 %q)", base64.StdEncoding.EncodeToString([]byte(line)))
		}
		if line != "" && globMatchFile(pattern, line, path, dir) {
			if dir != "" {
				line = searchLocator(line)
			}
			kept = append(kept, line)
		}
	}
	if offset >= int64(len(kept)) {
		return findZero(cfg, pc), nil
	}
	kept = kept[offset:]
	truncated := len(kept) > cfg.MaxMatches
	if truncated {
		kept = kept[:cfg.MaxMatches]
	}
	lines := make([]searchLine, 0, len(kept))
	for _, p := range kept {
		lines = append(lines, searchLine{prefix: p, match: true})
	}
	return pageText(lines, cfg, offset, truncated, pc)
}

// Keep root-file locators explicitly anchored when reused as a glob; a bare
// basename intentionally matches that name at any depth under rg semantics.
func searchLocator(file string) string {
	file = filepath.Clean(file)
	if !filepath.IsAbs(file) && !strings.ContainsRune(file, filepath.Separator) {
		return "./" + file
	}
	return filepath.ToSlash(file)
}

// FindFilesOutside reuses the bounded, non-following finder for an already
// admitted explicit outside root. Its locators remain absolute, never workspace-relative.
func FindFilesOutside(ctx context.Context, cfg config.SearchConfig, root string, args tool.Args, catalogs ...*prompts.Catalog) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	absolute := make(tool.Args, len(args))
	for key, value := range args {
		absolute[key] = value
	}
	absolute["path"] = root
	return runFind(ctx, cfg, absolute, root, catalogs...)
}

// Offset arithmetic is checked before any process can be spawned.
func pageOffset(args tool.Args, limit int64) (int64, error) {
	offset, _ := args["offset"].(int64)
	if offset < 0 || offset > math.MaxInt64-limit-1 {
		return 0, fmt.Errorf("offset must be nonnegative and offset+limit+1 must fit int64")
	}
	return offset, nil
}

type searchLine struct {
	prefix, body, path string
	number             int
	match, lf, cut     bool
}

// Only complete delivered locators advance the live-query cursor.
func pageText(lines []searchLine, cfg config.SearchConfig, offset int64, more bool, catalogs ...*prompts.Catalog) (tool.Result, error) {
	pc := prompts.Select(catalogs...)
	if len(lines) == 0 {
		return grepZero(cfg, pc), nil
	}
	grouped := false
	for _, l := range lines {
		if l.path != "" {
			grouped = true
			break
		}
	}
	clipped := false
	for i := range lines {
		l := &lines[i]
		room := cfg.MaxLineLength - len(l.prefix)
		if room < 0 {
			return tool.Result{}, fmt.Errorf("search locator exceeds max_line_length; %s", pc.Text("guidance.search.locator_cap"))
		}
		if len(l.body) > room {
			clipped, l.cut = true, true
			room = max(0, room-len("…[line cut]"))
			for room > 0 && !utf8.RuneStart(l.body[room]) {
				room--
			}
			l.body = l.body[:room]
			if len(l.prefix)+len(l.body)+len("…[line cut]") <= cfg.MaxLineLength {
				l.body += "…[line cut]"
			}
		}
	}
	// Preserve native locators; JSON escaping and group framing count toward bytes.
	piece := func(l searchLine, previous string) (string, string) {
		if !grouped {
			return l.prefix + l.body, previous
		}
		if l.path == "" {
			return "", previous
		} // textual gap separators become line-number gaps
		source, _ := json.Marshal(l.body)
		row := fmt.Sprintf(`{"line":%d,"source":%s,"match":%t,"lf":%t,"truncated":%t}`, l.number, source, l.match, l.lf, l.cut)
		prefix := ","
		if l.path != previous {
			file, _ := json.Marshal(l.path)
			prefix = `[{"path":` + string(file) + `,"lines":[`
			if previous != "" {
				prefix = `]},{"path":` + string(file) + `,"lines":[`
			}
		}
		return prefix + row, l.path
	}
	suffix := ""
	if grouped {
		suffix = "]}]"
	}
	var all strings.Builder
	previous := ""
	for _, l := range lines {
		text, next := piece(l, previous)
		if text != "" && !grouped && all.Len() > 0 {
			all.WriteByte('\n')
		}
		all.WriteString(text)
		previous = next
	}
	joined := all.String() + suffix
	if offset == 0 && !more && !clipped && len(joined) <= cfg.MaxBytes {
		return tool.Result{Text: joined}, nil
	}
	footer := func(next int64, remaining, truncated bool) string {
		prefix := "live page:"
		if truncated {
			prefix = fmt.Sprintf("results truncated: within %d matches / %dKB;", cfg.MaxMatches, cfg.MaxBytes/1024)
		}
		hint := ""
		if clipped {
			hint = "; [line cut] " + pc.Text("guidance.search.clipped_line")
		}
		return fmt.Sprintf("\n[%s offset=%d next_offset=%d more=%t; %s; files may shift; page-edge context may omit%s]", prefix, offset, next, remaining, pc.Text("guidance.search.repeat_page"), hint)
	}
	budget := cfg.MaxBytes - len(suffix) - len(footer(offset+int64(cfg.MaxMatches), false, true))
	var out strings.Builder
	delivered := int64(0)
	truncated := more || clipped
	previous = ""
	for _, l := range lines {
		text, next := piece(l, previous)
		if text == "" {
			continue
		}
		extra := len(text)
		if !grouped && out.Len() > 0 {
			extra++
		}
		if out.Len()+extra > budget && delivered == 0 && l.match {
			out.Reset()
			previous = ""
			text, next = piece(l, previous)
			extra = len(text)
		}
		if out.Len()+extra > budget {
			truncated = true
			if l.match {
				more = true
				break
			}
			continue
		}
		if !grouped && out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(text)
		previous = next
		if l.match {
			delivered++
		}
	}
	if delivered == 0 {
		return tool.Result{}, fmt.Errorf("search page cannot fit one complete locator plus recovery footer within max_bytes=%d; %s", cfg.MaxBytes, pc.Text("guidance.search.page_cap"))
	}
	return tool.Result{Text: out.String() + suffix + footer(offset+delivered, more, truncated), Truncated: truncated}, nil
}

// Separate framing, one aggregate budget; distinct os/exec pipe writers can race.
type searchCapture struct {
	mu        sync.Mutex
	buf       [2]bytes.Buffer
	remaining int
	overflow  bool
}
type captureWriter struct {
	owner  *searchCapture
	stream int
}

func (w captureWriter) Write(p []byte) (int, error) {
	c := w.owner
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	keep := min(n, c.remaining)
	_, _ = c.buf[w.stream].Write(p[:keep])
	c.remaining -= keep
	c.overflow = c.overflow || keep < n
	return n, nil // Drain excess without terminating the owned process.
}
func captureSearch(ctx context.Context, cmd *exec.Cmd, budget int, catalogs ...*prompts.Catalog) ([]byte, []byte, bool, error) {
	pc := prompts.Select(catalogs...)
	if budget <= 0 {
		return nil, nil, false, fmt.Errorf("tools.search.max_capture_bytes must be positive")
	}
	c := searchCapture{remaining: budget}
	cmd.Stdout = captureWriter{&c, 0}
	cmd.Stderr = captureWriter{&c, 1}
	err := process.Run(cmd)
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	if c.overflow {
		err = errors.Join(err, fmt.Errorf("raw search capture exceeded tools.search.max_capture_bytes=%d; no trustworthy page; %s", budget, pc.Text("guidance.search.capture_cap")))
	}
	return c.buf[0].Bytes(), c.buf[1].Bytes(), c.overflow, err
}

// Failed capture prefixes are evidence, never parsed pages; obey the result cap.
func rawPrefix(out, stderr []byte, limit int) string {
	if len(out) == 0 && len(stderr) == 0 {
		return ""
	}
	text := "[incomplete raw search output]\n"
	text = text[:min(len(text), max(0, limit))]
	for _, p := range [][]byte{out, stderr} {
		n := min(len(p), max(0, limit-len(text)))
		for n > 0 && n < len(p) && !utf8.RuneStart(p[n]) {
			n--
		}
		text += string(p[:n])
	}
	return text
}
