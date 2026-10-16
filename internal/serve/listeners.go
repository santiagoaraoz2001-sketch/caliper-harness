package serve

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Listener is one loopback TCP listener with its owning process, as the
// OS reports it. Discovery is read-only; PID and image are diagnostic only.
type Listener struct {
	PID   int
	Host  string
	Port  int
	Image string
}

// DiscoveredServer is a listener that answered like a model server.
// Kind is the detected wire shape, never a product guess: behavior
// (model listing, residency, stop) keys off the shape. The two driver
// identities (rulith, sidecar) are matched by their own health
// contracts, not by name or port.
type DiscoveredServer struct {
	URL    string
	Kind   string
	PID    int
	Image  string
	Health Health
}

// DiscoverServers enumerates this machine's loopback listeners and
// fingerprints each over HTTP. Nothing is hardcoded: any process that
// answers the Ollama, OpenAI, or sidecar health contracts on loopback —
// or is owned by the Rulith manager — is a model server here.
func DiscoverServers(ctx context.Context, client *http.Client) ([]DiscoveredServer, error) {
	listeners, err := loopbackListeners(ctx)
	if err != nil {
		return nil, err
	}
	byEndpoint := map[string]Listener{}
	var endpoints []string
	for _, l := range listeners {
		base := "http://" + net.JoinHostPort(l.Host, strconv.Itoa(l.Port))
		if _, dup := byEndpoint[base]; !dup {
			byEndpoint[base] = l
			endpoints = append(endpoints, base)
		}
	}
	sort.Strings(endpoints)
	rulithEndpoint := ""
	if install, ok := locateRulith(); ok {
		if st, err := RulithStatusNow(ctx, install); err == nil {
			rulithEndpoint = st.Endpoint
		}
	}
	found := make([]*DiscoveredServer, len(endpoints))
	var wg sync.WaitGroup
	for i, base := range endpoints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := byEndpoint[base]
			if SameEndpoint(base, rulithEndpoint) {
				h := probeRulithContext(ctx, client, base)
				found[i] = &DiscoveredServer{URL: h.APIBase, Kind: KindRulith, PID: l.PID, Image: l.Image, Health: h}
				return
			}
			kind, h, ok := fingerprint(ctx, client, base)
			if !ok {
				return
			}
			url := h.APIBase
			if url == "" {
				url = base
			}
			found[i] = &DiscoveredServer{URL: url, Kind: kind, PID: l.PID, Image: l.Image, Health: h}
		}()
	}
	wg.Wait()
	var out []DiscoveredServer
	for _, s := range found {
		if s != nil {
			out = append(out, *s)
		}
	}
	return out, ctx.Err()
}

// fingerprint identifies one listener by contract, most specific first:
// Ollama native, then OpenAI-compatible, then the decision sidecar.
func fingerprint(ctx context.Context, client *http.Client, base string) (string, Health, bool) {
	if h := probeOllamaContext(ctx, client, base); h.Up {
		h.Kind = KindOllama
		return KindOllama, h, true
	}
	if h := probeOpenAIContext(ctx, client, base); h.Up {
		h.Kind = KindOpenAI
		return KindOpenAI, h, true
	}
	if h := probeSidecarContext(ctx, client, base); h.Up {
		h.Kind = KindSidecar
		return KindSidecar, h, true
	}
	return "", Health{}, false
}

// SameEndpoint compares server identity, retaining host, transport and API prefix.
// A conventional /v1 suffix names the same API base; custom prefixes do not.
func SameEndpoint(a, b string) bool {
	normalize := func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ""
		}
		host := strings.ToLower(u.Hostname())
		if host == "localhost" {
			host = "127.0.0.1"
		}
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		prefix := strings.TrimSuffix(strings.TrimRight(u.EscapedPath(), "/"), "/v1")
		return u.Scheme + "://" + net.JoinHostPort(host, port) + prefix
	}
	na, nb := normalize(a), normalize(b)
	return na != "" && na == nb
}
