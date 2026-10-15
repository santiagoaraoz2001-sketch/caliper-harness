// Package fetch implements the fetch tool: URL → model-readable text.
// HTML becomes structured plain text (title header, block breaks, links
// kept, code blocks fenced verbatim); other text passes through capped.
// Binary content is refused plainly instead of dumped.
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/tool"
)

// Fetcher fetches with cfg limits. The client is swappable for tests.
type Fetcher struct {
	cfg     config.FetchConfig
	client  *http.Client
	prompts *prompts.Catalog
}

// New returns the fetch definition bound to cfg.
func New(cfg config.FetchConfig, catalogs ...*prompts.Catalog) tool.Definition {
	pc := prompts.Select(catalogs...)
	f := &Fetcher{cfg: cfg, prompts: pc, client: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}}
	return tool.Definition{Prompts: pc,
		Name:    "fetch",
		Label:   "fetch",
		Desc:    pc.Text("tools.fetch.description"),
		Purpose: pc.Text("tools.fetch.purpose"),
		Params: []tool.Param{
			{Name: "url", Desc: pc.Text("tools.fetch.parameters.url"), Type: tool.TypeString, Required: true},
		},
		Execute: func(ctx context.Context, args tool.Args) (tool.Result, error) {
			if err := ctx.Err(); err != nil {
				return tool.Result{}, err
			}
			u, _ := args["url"].(string)
			return f.Fetch(ctx, u)
		},
	}
}

// Fetch downloads url and returns capped text.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (tool.Result, error) {
	pc := prompts.Select(f.prompts)
	u, err := url.ParseRequestURI(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return tool.Result{}, fmt.Errorf("not a fetchable URL %q — %s", rawURL, pc.Text("guidance.fetch.invalid_url"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return tool.Result{}, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return tool.Result{}, fmt.Errorf("cannot fetch %s: %w — %s", u.Host, err, pc.Text("guidance.fetch.unreachable"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return tool.Result{}, fmt.Errorf("fetching %s returned %s", u.String(), resp.Status)
	}
	ctype := resp.Header.Get("Content-Type")
	if isBinary(ctype) {
		return tool.Result{}, fmt.Errorf("%s is %s, not text — %s", u.String(), mediaType(ctype), pc.Text("guidance.fetch.binary"))
	}
	body, overflow, readErr := readCapped(resp.Body, f.cfg.MaxBytes)
	var text string
	if strings.Contains(ctype, "html") || ctype == "" {
		text = ExtractText(body)
	} else {
		text = body
	}
	text = strings.TrimSpace(text)
	if readErr != nil {
		return tool.Result{Text: text, Truncated: overflow, Failed: true},
			fmt.Errorf("cannot read page from %s: %w — transfer is incomplete; %s", u.String(), readErr, pc.Text("guidance.fetch.incomplete"))
	}
	if overflow {
		text += fmt.Sprintf("\n[page truncated: showing first %dKB]", f.cfg.MaxBytes/1024)
		return tool.Result{Text: text, Truncated: true}, nil
	}
	return tool.Result{Text: text}, nil
}

func readCapped(r io.Reader, max int) (string, bool, error) {
	raw, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if len(raw) > max {
		return string(raw[:max]), true, err
	}
	return string(raw), false, err
}

func isBinary(ctype string) bool {
	mt := mediaType(ctype)
	if mt == "" || mt == "application/octet-stream" {
		return true
	}
	return !strings.HasPrefix(mt, "text/") &&
		mt != "application/json" && mt != "application/xml" &&
		!strings.HasSuffix(mt, "+json") && !strings.HasSuffix(mt, "+xml")
}

func mediaType(ctype string) string {
	if i := strings.Index(ctype, ";"); i >= 0 {
		ctype = ctype[:i]
	}
	return strings.ToLower(strings.TrimSpace(ctype))
}

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

// ExtractText converts HTML to structured plain text.
func ExtractText(page string) string {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return strings.TrimSpace(page)
	}
	var b, rendered strings.Builder
	var title string
	flushProse := func() {
		rendered.WriteString(normalizeProse(b.String()))
		b.Reset()
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			if strings.TrimSpace(n.Data) != "" {
				b.WriteString(collapse(n.Data))
				b.WriteString(" ")
			}
		case html.ElementNode:
			switch n.Data {
			case "script", "style", "noscript", "template":
				return
			case "title":
				title = collapse(textOf(n))
				return
			case "pre":
				flushProse()
				// Parsed pre text bypasses prose/link transforms and normalization.
				payload := textOf(n)
				length, run := 3, 0
				for i := 0; i < len(payload); i++ {
					if payload[i] == '`' {
						run++
						if run >= length {
							length = run + 1
						}
					} else {
						run = 0
					}
				}
				fence := strings.Repeat("`", length)
				rendered.WriteString("\n" + fence + "\n" + payload + "\n" + fence + "\n")
				return
			case "br", "p", "div", "section", "article", "header", "footer",
				"h1", "h2", "h3", "h4", "h5", "h6", "li", "tr", "blockquote":
				b.WriteString("\n")
			case "a":
				href := attr(n, "href")
				label := collapse(textOf(n))
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c)
				}
				if label != "" && href != "" && href != label {
					b.WriteString("(" + href + ") ")
				}
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	flushProse()
	out := rendered.String()
	if title != "" {
		out = "# " + strings.TrimSpace(title) + "\n\n" + strings.TrimSpace(out)
	}
	return strings.TrimSpace(out)
}

func normalizeProse(text string) string {
	out := strings.TrimSpace(text)
	// Normalize blank runs to at most one empty line.
	lines := strings.Split(out, "\n")
	var kept []string
	blanks := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blanks++
			if blanks > 1 {
				continue
			}
			kept = append(kept, "")
			continue
		}
		blanks = 0
		kept = append(kept, strings.TrimRight(l, " \t"))
	}
	return strings.Join(kept, "\n")
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(x *html.Node) {
		if x.Type == html.ElementNode && (x.Data == "script" || x.Data == "style" || x.Data == "noscript" || x.Data == "template") {
			return
		}
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
