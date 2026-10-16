package serve

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
)

// Listener is one loopback TCP listener with its owning process, as the
// OS reports it. Image and Cmdline are best-effort: they are what make
// stop-by-PID and relaunch possible without knowing the product.
type Listener struct {
	PID     int
	Port    int
	Image   string
	Cmdline []string
}

// DiscoveredServer is a listener that answered like a model server.
// Kind is the detected wire shape, never a product guess: behavior
// (model listing, residency, stop) keys off the shape. The two driver
// identities (rulith, sidecar) are matched by their own health
// contracts, not by name or port.
type DiscoveredServer struct {
	URL     string
	Kind    string
	PID     int
	Image   string
	Cmdline []string
	Health  Health
}

// DiscoverServers enumerates this machine's loopback listeners and
// fingerprints each over HTTP. Nothing is hardcoded: any process that
// answers the Ollama, OpenAI, or sidecar health contracts on loopback —
// or is owned by the Rulith manager — is a model server here.
func DiscoverServers(ctx context.Context, client *http.Client) ([]DiscoveredServer, error) {
	listeners, err := loopbackListeners()
	if err != nil {
		return nil, err
	}
	byPort := map[int]Listener{}
	var ports []int
	for _, l := range listeners {
		if _, dup := byPort[l.Port]; !dup {
			byPort[l.Port] = l
			ports = append(ports, l.Port)
		}
	}
	sort.Ints(ports)
	rulithPort := 0
	if install, ok := locateRulith(); ok {
		if st, err := RulithStatusNow(ctx, install); err == nil {
			rulithPort = portOf(st.Endpoint)
		}
	}
	found := make([]*DiscoveredServer, len(ports))
	var wg sync.WaitGroup
	for i, port := range ports {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := byPort[port]
			base := fmt.Sprintf("http://127.0.0.1:%d", port)
			if port == rulithPort && rulithPort != 0 {
				h := probeRulithContext(ctx, client, base)
				found[i] = &DiscoveredServer{URL: h.APIBase, Kind: KindRulith, PID: l.PID, Image: l.Image, Cmdline: l.Cmdline, Health: h}
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
			found[i] = &DiscoveredServer{URL: url, Kind: kind, PID: l.PID, Image: l.Image, Cmdline: l.Cmdline, Health: h}
		}()
	}
	wg.Wait()
	var out []DiscoveredServer
	for _, s := range found {
		if s != nil {
			out = append(out, *s)
		}
	}
	return out, nil
}

// fingerprint identifies one listener by contract, most specific first:
// Ollama native, then OpenAI-compatible, then the decision sidecar.
func fingerprint(ctx context.Context, client *http.Client, base string) (string, Health, bool) {
	if h := probeOllamaContext(ctx, client, base); h.Up {
		h.Kind = KindOllama
		return KindOllama, h, true
	} else if !tryNext(h.Err) && h.Reachable {
		// A sick Ollama stays labeled Ollama (mirrors ProbeContext).
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

func portOf(rawURL string) int {
	var port int
	if rawURL == "" {
		return 0
	}
	host := rawURL
	for i := 0; i < len(host); i++ {
		if host[i] == '/' && i+1 < len(host) && host[i+1] == '/' {
			host = host[i+2:]
			break
		}
	}
	for i := 0; i < len(host); i++ {
		if host[i] == '/' {
			host = host[:i]
			break
		}
	}
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			for _, c := range host[i+1:] {
				if c < '0' || c > '9' {
					return 0
				}
				port = port*10 + int(c-'0')
			}
			return port
		}
	}
	return 0
}

// StopDiscovered stops a server found running. Driver-owned servers
// stop through their driver (Rulith persists conversations on manager
// stop); everything else stops by terminating the listening process.
// Loopback only: a discovered server is local by construction.
func StopDiscovered(ctx context.Context, s DiscoveredServer) error {
	if s.Kind == KindRulith {
		if install, ok := locateRulith(); ok {
			return RulithStop(ctx, install)
		}
	}
	if s.PID <= 0 {
		return fmt.Errorf("no process identity for %s; stop it where you started it", s.URL)
	}
	proc, err := os.FindProcess(s.PID)
	if err != nil {
		return fmt.Errorf("cannot address pid %d: %w", s.PID, err)
	}
	if err := proc.Kill(); err != nil {
		return fmt.Errorf("could not stop pid %d: %w", s.PID, err)
	}
	return nil
}

// RelaunchRecipe rebuilds a start recipe from a captured command line:
// how a previously seen server comes back without anyone hardcoding
// how it launches. The working directory is not captured; servers that
// need one belong in serve.recipes with an explicit dir.
func RelaunchRecipe(s DiscoveredServer) (Recipe, bool) {
	if len(s.Cmdline) == 0 {
		return Recipe{}, false
	}
	return Recipe{Name: "relaunched", Kind: s.Kind, URL: s.URL, Argv: append([]string(nil), s.Cmdline...)}, true
}
