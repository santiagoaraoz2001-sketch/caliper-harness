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
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	req, err := json.Marshal(map[string]any{"op": op, "data": data})
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, install.Python, install.Manager)
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
			ID       string   `json:"id"`
			Path     string   `json:"path"`
			Name     string   `json:"name"`
			Role     string   `json:"role"`
			Missing  []string `json:"missing"`
			Filename string   `json:"filename"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &cat); err != nil {
		return nil, fmt.Errorf("rulith catalog unreadable: %v", err)
	}
	var out []Asset
	for _, m := range cat.Models {
		a := Asset{Path: m.Path, ID: m.ID, Runtime: "rulith"}
		switch {
		case m.Role != "model":
			a.Reason = "rulith " + m.Role + " asset, not a loadable model"
		case len(m.Missing) > 0:
			a.Reason = "missing shards: " + strings.Join(m.Missing, ", ")
		default:
			a.Complete = true
			a.Reason = "rulith catalog model; load starts it on the Rulith server"
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
		h.Reachable, h.Up = true, true
		if st.ModelName != "" {
			h.Loaded = []string{st.ModelName}
		}
		for _, m := range st.ServedModels {
			if m.ID != "" && m.ID != st.ModelName {
				h.Available = append(h.Available, m.ID)
			}
		}
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
