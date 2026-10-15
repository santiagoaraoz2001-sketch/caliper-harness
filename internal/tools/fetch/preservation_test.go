package fetch

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"specific-harness/internal/config"
	"specific-harness/internal/tool"
)

func registeredFetch(t *testing.T, page, contentType string, maxBytes int, broken bool) (tool.Result, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/page" {
			t.Errorf("wrong fixture path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", contentType)
		if broken {
			w.Header().Set("Content-Length", "99999")
		}
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()
	d := New(config.FetchConfig{TimeoutSeconds: 5, MaxBytes: maxBytes})
	args, repair := tool.Validate(d, map[string]string{"url": srv.URL + "/page"})
	if repair != "" {
		t.Fatal(repair)
	}
	return d.Execute(context.Background(), args)
}
func TestFetchPreservesParsedPre(t *testing.T) {
	whitespace := " \tif café:\n\tpass  \t\n \t\n\n\nLAST \t "
	fence3 := "BEFORE\n```\n  literal\t \n```\nAFTER"
	fence5 := "a ````` b\n````\nend  "
	for _, tc := range []struct{ name, page, payload, fence string }{
		{"significant whitespace", "<pre>" + html.EscapeString(whitespace) + "</pre>", whitespace, "```"},
		{"nested code entities and anchor", `<pre><code>&lt;x&gt;&amp;&#9;  ` + "\n" + `<span>  &quot;part&quot; </span><a href="https://should-not-decorate.invalid">raw&#9;</a>` + "\n\n</code></pre>", "<x>&\t  \n  \"part\" raw\t\n\n", "```"},
		{"literal three ticks", "<pre>" + html.EscapeString(fence3) + "</pre>", fence3, "````"},
		{"longer literal run", "<pre>" + html.EscapeString(fence5) + "</pre>", fence5, "``````"},
		{"whitespace only", "<pre> \t\n\n \t </pre>", " \t\n\n \t ", "```"},
		// These are the existing HTML parser's decoded-text semantics, not raw HTML bytes.
		{"HTML newline semantics", "<pre>\nfirst\r\n\tx  \r\n</pre>", "first\n\tx  \n", "```"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := registeredFetch(t, tc.page, "text/html", 65536, false)
			want := tc.fence + "\n" + tc.payload + "\n" + tc.fence
			if err != nil || res.Failed || res.Truncated || res.Text != want {
				t.Fatalf("parsed pre bytes/boundaries changed\nwant=%q\ngot=%q flags=%+v err=%v", want, res.Text, res, err)
			}
		})
	}
}
func TestFetchPreservesOriginalFixtureAndProse(t *testing.T) {
	// Byte-identical original tool-controls/run-1/http-bodies/html.body, 2026-10-09.
	page := `<!doctype html><html><head><title>Owned fixture title</title><style>HIDE_STYLE</style></head><body><p>Paragraph one.</p><a href='https://example.invalid/docs'>Reference</a><pre>def greet():` + "\n    return &quot;hello&quot;  \n\n\n    # retained indent\n" + `</pre><script>HIDE_SCRIPT</script></body></html>`
	res, err := registeredFetch(t, page, "text/html", 65536, false)
	payload := "def greet():\n    return \"hello\"  \n\n\n    # retained indent\n"
	want := "# Owned fixture title\n\nParagraph one. Reference (https://example.invalid/docs)\n```\n" + payload + "\n```"
	if err != nil || res.Text != want || res.Failed || res.Truncated {
		t.Fatalf("original fixture lost bytes: got%q want%q err%v", res.Text, want, err)
	}
	page = `<title> Multi   title </title><p>Before   prose.</p><pre>first  ` + "\n\n" + `</pre><p>Between   prose.</p><pre><code>` + "\tsecond \t" + `</code></pre><p>After   prose. <a href="https://example.invalid/docs">Docs</a></p>`
	res, err = registeredFetch(t, page, "text/html", 65536, false)
	for _, part := range []string{"# Multi title\n\nBefore prose.", "```\nfirst  \n\n\n```", "Between prose.", "```\n\tsecond \t\n```", "After prose. Docs (https://example.invalid/docs)"} {
		if !strings.Contains(res.Text, part) {
			t.Errorf("multiple blocks/prose missing%q got%q", part, res.Text)
		}
	}
	if err != nil || res.Failed || res.Truncated {
		t.Fatalf("normal extraction flags/error %+v %v", res, err)
	}
}
func TestFetchPreservesCappedAndFailedPre(t *testing.T) {
	const prefix = "<pre>KEEP\t \n\n"
	res, err := registeredFetch(t, prefix+"AFTER</pre>", "text/html", len(prefix), false)
	if err != nil || res.Failed || !res.Truncated || !strings.Contains(res.Text, "```\nKEEP\t \n\n\n```\n[page truncated:") || strings.Contains(res.Text, "AFTER") {
		t.Fatalf("cap lost partial bytes/disclosure %+v %v", res, err)
	}
	res, err = registeredFetch(t, "<pre>partial\t  \n\n\nx  </pre>", "text/html", 65536, true)
	if err == nil || !strings.Contains(err.Error(), "unexpected EOF") || !strings.Contains(err.Error(), "/page") || !res.Failed || res.Truncated || res.Text != "```\npartial\t  \n\n\nx  \n```" {
		t.Fatalf("incomplete transfer lost failed/partial evidence %+v %v", res, err)
	}
	res, err = registeredFetch(t, " \tplain \t\n\n", "text/plain", 65536, false)
	if err != nil || res.Text != "plain" || res.Failed || res.Truncated {
		t.Fatalf("non-HTML behavior changed %+v %v", res, err)
	}
}

func TestFetchPreservesPreInsideLink(t *testing.T) {
	const payload = " \twrapped  \n\n\nx \t "
	page := `<a href="https://example.invalid/wrapped"><pre>` + payload + `</pre></a>`
	res, err := registeredFetch(t, page, "text/html", 65536, false)
	want := "```\n" + payload + "\n```\n(https://example.invalid/wrapped)"
	if err != nil || res.Text != want || res.Failed || res.Truncated {
		t.Fatalf("outer anchor rewrote parsed pre: got%q want%q err%v", res.Text, want, err)
	}
	for _, tc := range []struct{ page, want string }{
		{`<a href="https://go.dev"><span>Go   site</span></a>`, "Go site (https://go.dev)"},
		{`<a href="https://go.dev">https://go.dev</a>`, "https://go.dev"},
		{`<a href="https://go.dev"> \t </a>`, `\t (https://go.dev)`},
		{"<a href=\"https://go.dev\"> \t </a>", ""},
		{`<a href="https://go.dev"><script>HIDE</script><style>HIDE</style><noscript>HIDE</noscript><template>HIDE</template></a>`, ""},
	} {
		res, err = registeredFetch(t, tc.page, "text/html", 65536, false)
		if err != nil || res.Text != tc.want {
			t.Errorf("ordinary link changed: got%q want%q err%v", res.Text, tc.want, err)
		}
	}
}
