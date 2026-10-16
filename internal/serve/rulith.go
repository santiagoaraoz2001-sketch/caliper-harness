package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RulithInstall locates one Rulith Inference installation: the embedded
// Python and its manager. The manager speaks one JSON request on stdin
// for one JSON result; inference itself is OpenAI-compatible HTTP.
type RulithInstall struct {
	Root    string // install root (…\Rulith Inference)
	Python  string // runtime\python\python.exe
	Manager string // runtime\tools\manager.py
}

// EndpointHint is where the server will listen before status confirms
// it: RULITH_PORT wins, else the product default 8080.
func (i RulithInstall) EndpointHint() string {
	port := strings.TrimSpace(os.Getenv("RULITH_PORT"))
	if port == "" {
		port = "8080"
	}
	return "http://127.0.0.1:" + port + "/v1"
}

type rulithStatus struct {
	Status           string `json:"status"` // stopped, loading, ready
	Endpoint         string `json:"endpoint"`
	ModelName        string `json:"model_name"`
	Failure          string `json:"failure"`
	RuntimeAvailable bool   `json:"runtime_available"`
	ServedModels     []struct {
		ID string `json:"id"`
	} `json:"served_models"`
}

// rulithCall runs one manager operation. The manager prints its JSON
// result even when it exits non-zero, so stdout is the contract.
func rulithCall(ctx context.Context, install RulithInstall, op string, data map[string]any) (json.RawMessage, error) {
	req, err := json.Marshal(map[string]any{"op": op, "data": data})
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, install.Python, install.Manager)
	// Match process.Run settlement, without killing manager-owned server children.
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(req)
	out, runErr := cmd.Output()
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
		Code  string          `json:"code"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("rulith manager %s: %v", op, runErr)
		}
		return nil, fmt.Errorf("rulith manager %s answered garbage: %v", op, err)
	}
	if !envelope.OK {
		msg := envelope.Error
		if envelope.Code != "" {
			msg = envelope.Code + ": " + msg
		}
		return nil, fmt.Errorf("rulith %s refused: %s", op, msg)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if runErr != nil {
		return nil, fmt.Errorf("rulith manager %s failed despite receipt: %w", op, runErr)
	}
	return envelope.Data, nil
}

// RulithStatusNow reads the manager's status (no model is loaded by asking).
func RulithStatusNow(ctx context.Context, install RulithInstall) (rulithStatus, error) {
	raw, err := rulithCall(ctx, install, "status", map[string]any{})
	if err != nil {
		return rulithStatus{}, err
	}
	var st rulithStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return rulithStatus{}, fmt.Errorf("rulith status unreadable: %v", err)
	}
	return st, nil
}

// RulithAssets lists the manager's model catalog as load candidates.
// Only role=model entries with no missing shards are complete.
func RulithAssets(ctx context.Context, install RulithInstall) ([]Asset, error) {
	raw, err := rulithCall(ctx, install, "catalog", map[string]any{})
	if err != nil {
		return nil, err
	}
	var cat struct {
		Models []struct {
			ID       string    `json:"id"`
			Path     string    `json:"path"`
			Name     string    `json:"name"`
			Role     string    `json:"role"`
			Missing  *[]string `json:"missing"`
			Filename string    `json:"filename"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("rulith catalog unreadable: %v", err)
	}
	var out []Asset
	for _, m := range cat.Models {
		a := Asset{Path: m.Path, ID: m.ID, Runtime: "rulith", Model: m.Name}
		switch {
		case strings.TrimSpace(m.ID) == "" || strings.TrimSpace(m.Path) == "":
			a.Reason = "rulith catalog omitted model identity/path"
		case m.Role != "model":
			a.Reason = "rulith " + m.Role + " asset, not a loadable model"
		case m.Missing == nil:
			a.Reason = "rulith catalog omitted shard completeness"
		case len(*m.Missing) > 0:
			a.Reason = "missing shards: " + strings.Join(*m.Missing, ", ")
		default:
			a.Complete = true
			a.Reason = "rulith catalog reports complete model; runtime compatibility unconfirmed"
		}
		out = append(out, a)
	}
	return out, nil
}

// RulithStart asks the manager to load one catalog model. Readiness is
// confirmed by polling status, never assumed from acceptance.
func RulithStart(ctx context.Context, install RulithInstall, modelID string) error {
	_, err := rulithCall(ctx, install, "start", map[string]any{"id": modelID})
	return err
}

// RulithStop asks the manager to stop its server (saved conversations persist).
func RulithStop(ctx context.Context, install RulithInstall) error {
	_, err := rulithCall(ctx, install, "stop", map[string]any{})
	return err
}

// locateRulith is a seam so tests pin install presence deterministically.
var locateRulith = LocateRulith

// probeRulithContext reports manager truth: stopped/loading/ready plus
// the authoritative endpoint. A missing install reads as down, loudly.
func probeRulithContext(ctx context.Context, client *http.Client, base string) Health {
	if _, ok := ctx.Deadline(); !ok && client.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, client.Timeout)
		defer cancel()
	}
	install, ok := locateRulith()
	if !ok {
		return Health{Err: "rulith not installed (no manager found)"}
	}
	start := time.Now()
	st, err := RulithStatusNow(ctx, install)
	if err != nil {
		return Health{Err: err.Error()}
	}
	h := Health{Runtime: "rulith", LatencyMs: time.Since(start).Milliseconds()}
	endpoint := st.Endpoint
	if endpoint == "" {
		endpoint = install.EndpointHint()
	}
	h.APIBase = endpoint
	switch st.Status {
	case "ready":
		if !SameEndpoint(base, endpoint) || !Loopback(endpoint) {
			h.Err = "rulith endpoint changed; refresh before use"
			return h
		}
		wire := probeOpenAIContext(ctx, client, endpoint)
		h.Reachable = wire.Reachable
		if !wire.Up {
			h.Err = "rulith reports ready but HTTP catalog is unavailable: " + wire.Err
			return h
		}
		h.Available = wire.Available
		for _, name := range wire.Available {
			if name == st.ModelName && name != "" {
				h.Up = true
				h.Loaded = []string{name}
				return h
			}
		}
		h.Err = "rulith ready model is absent from HTTP catalog"
	case "loading":
		h.Reachable = true
		h.Err = "rulith is loading " + st.ModelName
	default: // stopped
		if st.Failure != "" {
			h.Err = "rulith stopped; last failure: " + st.Failure
		} else {
			h.Err = "rulith stopped — load a model to start it"
		}
	}
	return h
}

// SetRulithResident uses the manager's catalog identity and confirms its actual
// status plus served HTTP inventory. Acceptance alone is not residency.
func SetRulithResident(ctx context.Context, client *http.Client, install RulithInstall, endpoint, id string, load bool) (Health, error) {
	assets, err := RulithAssets(ctx, install)
	if err != nil {
		return Health{}, err
	}
	var selected *Asset
	for i := range assets {
		if assets[i].ID == id && assets[i].Complete {
			selected = &assets[i]
			break
		}
	}
	if selected == nil {
		return Health{}, fmt.Errorf("rulith model %q absent or incomplete in current catalog", id)
	}
	model := AssetModelName(*selected)
	st, err := RulithStatusNow(ctx, install)
	if err != nil {
		return Health{}, err
	}
	base := st.Endpoint
	if base == "" {
		base = install.EndpointHint()
	}
	if !Loopback(endpoint) || !SameEndpoint(endpoint, base) {
		return Health{}, fmt.Errorf("rulith endpoint changed; refresh before resident action")
	}
	if st.Status == "loading" || st.Status == "ready" && st.ModelName != model {
		return Health{}, fmt.Errorf("rulith is already serving/loading another model; release it first")
	}
	if load {
		err = RulithStart(ctx, install, id)
	} else {
		err = RulithStop(ctx, install)
	}
	if err != nil {
		return Health{}, err
	}
	for {
		st, err = RulithStatusNow(ctx, install)
		if err != nil {
			return Health{}, err
		}
		if !load && st.Status == "stopped" {
			return Health{Kind: KindRulith, Runtime: "rulith", APIBase: endpoint}, nil
		}
		if load && st.Status == "ready" {
			if st.ModelName != model || !SameEndpoint(st.Endpoint, endpoint) {
				return Health{}, fmt.Errorf("rulith ready identity differs from selected model/endpoint")
			}
			h := probeOpenAIContext(ctx, client, endpoint)
			if h.Up {
				for _, name := range h.Available {
					if name == model {
						h.Kind, h.Runtime, h.Loaded = KindRulith, "rulith", []string{model}
						return h, nil
					}
				}
			}
		}
		if st.Failure != "" {
			return Health{}, fmt.Errorf("rulith failed: %s", st.Failure)
		}
		select {
		case <-ctx.Done():
			return Health{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
