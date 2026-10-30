package run

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"specific-harness/internal/config"
	"specific-harness/internal/memory"
	"specific-harness/internal/session"
	"specific-harness/internal/store"
)

type projectContextItem struct {
	Key               string             `json:"key"`
	ID                string             `json:"id,omitempty"`
	TaskID            string             `json:"taskId,omitempty"`
	Section           string             `json:"section,omitempty"`
	Source            string             `json:"source"`
	Status            string             `json:"status"`
	Body              string             `json:"body"`
	Links             []memory.Reference `json:"links,omitempty"`
	Selection         string             `json:"selection,omitempty"`
	DomainBudgetBasis string             `json:"domainBudgetBasis,omitempty"`
	domainLimit       int
}

type projectContextPacket struct {
	Project        string               `json:"project,omitempty"`
	Items          []projectContextItem `json:"items,omitempty"`
	TaskKnowledge  string               `json:"taskKnowledge,omitempty"`
	Recall         string               `json:"recall,omitempty"`
	RecallStatus   string               `json:"recallStatus,omitempty"`
	Status         string               `json:"status,omitempty"`
	GuidanceStatus string               `json:"guidanceStatus,omitempty"`
}

const projectContextMarker = "<project_knowledge>\n"

// projectContext projects current scoped facts into the existing conversation.
// It runs at complete response boundaries, never during raw continuation. The
// three small entry sections orient unclassified work; deeper bodies stay lazy.
func projectContext(cfg config.Config, s *store.Store, root string, count memory.TokenCounter, recalls ...*projectRecall) func(context.Context, *session.Continuation, string) error {
	return func(ctx context.Context, c *session.Continuation, taskID string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var previous projectContextPacket
		old := -1
		for i, entry := range c.Entries {
			if entry.Kind == "injected" && strings.HasPrefix(entry.Message.Content, projectContextMarker) {
				old = i
				_ = json.Unmarshal([]byte(strings.TrimPrefix(entry.Message.Content, projectContextMarker)), &previous)
			}
		}
		packet := projectContextPacket{Project: root}
		requests := []projectContextItem{{Key: "project/design/identity"}, {Key: "project/testing/strategy"}}
		// The caller supplies an admitted live task/review identity. Historical
		// CodingState is not a selector and cannot create an execution owner.
		if taskID != "" {
			var entries []*memory.Entry
			var err error
			if s == nil {
				err = errors.New("project store unavailable")
			} else {
				entries, err = memory.SearchKnowledge(ctx, s, root, "", "", cfg.Tools.GraphQL.MaxRows+1, cfg.Tools.GraphQL.MaxResultBytes, false, []string{"key", "taskId"}, memory.KnowledgeSelection{TaskID: taskID})
			}
			packet.TaskKnowledge = "absent"
			if err != nil {
				packet.TaskKnowledge = "unavailable"
				if errors.Is(err, memory.ErrSourceBudget) {
					packet.TaskKnowledge = "omitted-budget"
				}
			} else if len(entries) > cfg.Tools.GraphQL.MaxRows {
				packet.TaskKnowledge = "omitted-budget"
			} else {
				for _, entry := range entries {
					packet.TaskKnowledge = "selected"
					if entry.Key != requests[0].Key && entry.Key != requests[1].Key {
						requests = append(requests, projectContextItem{Key: entry.Key, ID: entry.ID, TaskID: entry.TaskID})
					}
				}
			}
		}
		for _, request := range requests {
			key := request.Key
			item := projectContextItem{Key: key, Source: "project-authored", Status: "absent"}
			if request.ID != "" {
				key, item.TaskID = "", request.TaskID
			}
			if s == nil {
				item.Status = "unavailable"
			} else if entries, err := memory.Knowledge(ctx, s, root, key, request.ID, 1, cfg.Tools.GraphQL.MaxResultBytes, false, []string{}); err != nil {
				item.Status = "unavailable"
				if errors.Is(err, memory.ErrSourceBudget) {
					item.Status = "omitted-budget"
				}
			} else if len(entries) > 0 {
				item.ID, item.Status = entries[0].ID, "omitted-budget"
				fetched := false
				if previous.Project == root {
					for _, cached := range previous.Items {
						if cached.Key == item.Key && cached.ID == item.ID && cached.Status == "supplied" && cached.Selection == "" && cached.Section == "" {
							item.Body = cached.Body
							fetched = true
							break
						}
					}
				}
				if !fetched {
					bytes := cfg.Tools.GraphQL.MaxResultBytes
					if bodies, err := memory.Knowledge(ctx, s, root, "", item.ID, 1, bytes, true, []string{}); err != nil {
						if !errors.Is(err, memory.ErrSourceBudget) {
							item.Status = "unavailable"
						}
					} else if len(bodies) > 0 {
						item.Body = bodies[0].Body
						fetched = true
					}
				}
				if fetched {
					item.Status = "supplied"
				} else {
					item.Body = ""
				}
				// Explicit task-note links are offered for selective next hops;
				// target bodies are never recursively loaded into the prompt.
				if item.TaskID != "" {
					links, err := memory.References(ctx, s, root, item.ID, false, cfg.Tools.GraphQL.MaxResultBytes, nil)
					if err != nil {
						packet.TaskKnowledge = "references-unavailable"
					} else {
						item.Links = links
					}
				}
			}
			if memory.ProjectGuide(item.Key) && item.Status == "supplied" {
				item.Body, item.Status = "", "select-section"
			}
			packet.Items = append(packet.Items, item)
		}
		// Explicit owner selections are pinned bodies, not a list of suggestions.
		// Resolve one level only; links remain available for the next graph hop.
		refs, selectionErr := selectedGuidance(s, root, c, taskID)
		encodedSelection, _ := json.Marshal(refs)
		if len(refs) > cfg.Tools.GraphQL.MaxRows || len(encodedSelection) > cfg.Tools.GraphQL.MaxResultBytes {
			selectionErr = errors.New("selected guidance exceeds configured source row/byte budget; select fewer sections")
			refs = nil
		}
		if selectionErr != nil {
			packet.GuidanceStatus = selectionErr.Error()
		}
		for _, ref := range refs {
			if !cfg.Testing.Guidance && testingGuidance(ref.Key) {
				packet.Items = append(packet.Items, projectContextItem{Key: ref.Key, ID: ref.Version, Section: ref.Section, Selection: "pinned", Status: "disabled"})
				continue
			}
			item, err := guidanceItem(ctx, cfg, s, root, ref)
			item.Selection = "pinned"
			if err == nil && !domainContextItem(item) {
				for _, owner := range packet.Items {
					if owner.Key == item.Key && owner.ID == item.ID && owner.Status == "supplied" && owner.Selection == "" && owner.Section == "" {
						// The whole immutable owner already includes this section.
						// Preserve the pin and links without repeating its body.
						item.Body, item.Status = "", "supplied-in-owner"
						break
					}
				}
			}
			if err == nil {
				err = admitGuidanceItem(ctx, count, &item)
			}
			if err != nil {
				item.Body, item.Status = "", "unavailable"
				packet.GuidanceStatus = err.Error()
			}
			packet.Items = append(packet.Items, item)
		}
		pinnedDomain := false
		for _, item := range packet.Items {
			pinnedDomain = pinnedDomain || (item.Selection == "pinned" && item.Status == "supplied" && item.Body != "" && domainContextItem(item))
		}
		for _, entry := range []struct{ key, section string }{{memory.CodeStandardKey, "scope"}, {memory.UIStandardKey, "identity"}, {memory.TestsStandardKey, "intent"}} {
			// Explicit domain bodies take over orientation after selection. The
			// generic roots have already introduced their lazy graph entry points.
			if pinnedDomain {
				break
			}
			if entry.key == memory.TestsStandardKey && !cfg.Testing.Guidance {
				continue
			}
			node := memory.BuiltinKnowledge(entry.key, "", "")
			for _, part := range memory.Sections(node.Body, "", entry.section) {
				already := false
				for _, item := range packet.Items {
					already = already || (item.ID == node.ID && item.Section == part.ID)
				}
				if already {
					continue
				}
				// The authored section already carries its exact Markdown next-hop.
				// Public section references stay structured; do not inject each link twice.
				item := projectContextItem{Key: entry.key, ID: node.ID, Section: part.ID, Source: "caliper-default", Status: "supplied", Body: part.Body}
				if err := admitGuidanceItem(ctx, count, &item); err != nil {
					item.Body, item.Status = "", "unavailable"
					packet.GuidanceStatus = err.Error()
				}
				packet.Items = append(packet.Items, item)
			}
		}
		omitted, err := boundGuidanceItems(ctx, count, packet.Items)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, context.Canceled) {
				return err
			}
			// A domain delivery failure cannot withhold ordinary current facts.
			// Remove even domain locators when their envelope cannot be admitted;
			// no uncounted domain payload or alternate counting policy is emitted.
			kept := packet.Items[:0]
			for _, item := range packet.Items {
				if !domainContextItem(item) {
					kept = append(kept, item)
				}
			}
			packet.Items = kept
			packet.GuidanceStatus = "domain guidance unavailable; domain items omitted: " + err.Error()
		}
		if omitted {
			packet.GuidanceStatus = "domain guidance exceeds 2000-unit context budget; omitted-budget items retain exact locators; select fewer or narrower sections"
		}
		raw, _ := json.Marshal(packet)
		if len(raw) > cfg.Tools.GraphQL.MaxResultBytes {
			for i := range packet.Items {
				if packet.Items[i].Status == "supplied" || packet.Items[i].Status == "supplied-in-owner" {
					packet.Items[i].Status = "omitted-budget"
				}
				packet.Items[i].Body, packet.Items[i].Links = "", nil
			}
			raw, _ = json.Marshal(packet)
			if len(raw) > cfg.Tools.GraphQL.MaxResultBytes {
				packet = projectContextPacket{Status: "omitted-budget"}
				raw, _ = json.Marshal(packet)
			}
		}
		if len(recalls) > 0 && recalls[0] != nil {
			// Automatic recall keeps its existing independent character budget;
			// the combined request remains subject to ordinary context admission.
			var err error
			packet.Recall, packet.RecallStatus, err = recalls[0].project(ctx, cfg, s, root, packet, previous)
			if err != nil {
				return err
			}
			raw, _ = json.Marshal(packet)
		}
		content := projectContextMarker + string(raw)
		if old >= 0 && c.Entries[old].Message.Content == content {
			return nil
		}
		if old >= 0 {
			c.Entries = append(c.Entries[:old], c.Entries[old+1:]...)
		}
		// Publish changed current facts at the complete-response boundary. Moving
		// the replacement to the tail preserves the unchanged conversation prefix;
		// it does not manufacture a tool receipt or modify raw continuation.
		c.Add("injected", "user", content, false)
		c.Invalidate()
		return nil
	}
}

// Only the main factory supplies automatic topical recall. Worker/reviewer
// projections remain limited to their current project/task facts.
type projectRecall struct {
	records                 []memory.RecallRecord
	query                   string
	projection              string
	text, status, published string
	initialized             bool
	warn                    func(error)
	publish                 func(context.Context, string) error
}

func (r *projectRecall) project(ctx context.Context, cfg config.Config, s *store.Store, root string, packet, previous projectContextPacket) (string, string, error) {
	heads := map[string]projectContextItem{}
	supplied := map[string]bool{}
	for _, item := range packet.Items {
		if item.Source != "project-authored" || item.Selection != "" {
			continue
		}
		heads[item.Key] = projectContextItem{ID: item.ID, Status: item.Status}
		if item.Status == "supplied" && item.ID != "" {
			supplied[item.ID] = true
		}
	}
	raw, _ := json.Marshal(heads)
	projection := string(raw)
	if !r.initialized || r.projection != projection {
		var entries []*memory.Entry
		var err error
		if s == nil && cfg.Recall.TopN > 0 {
			err = errors.New("project store unavailable")
		} else {
			entries, err = memory.ProjectRecallEntries(ctx, s, root, "", r.query, cfg.Recall.TopN, cfg.Recall.MinWord)
		}
		var cached []memory.RecallRecord
		r.status = ""
		if err != nil {
			if r.warn != nil {
				r.warn(err)
			}
			cached = r.records
			r.status = "unavailable"
			// These facts were already supplied in the preceding bounded packet.
			// They are cached evidence now, not a successful current lookup.
			if previous.Project == root {
				for _, item := range previous.Items {
					if item.Source == "project-authored" && item.Status == "supplied" && item.ID != "" {
						entries = append(entries, &memory.Entry{ID: item.ID, Key: item.Key, ProjectRoot: root, Kind: "note", Body: item.Body})
					}
				}
			}
		}
		keep := func(e *memory.Entry) bool {
			if supplied[e.ID] {
				return false
			}
			head, known := heads[e.Key]
			// A failed refresh cannot reintroduce a known retired head.
			return !(known && e.ProjectRoot == root && (head.ID != "" && head.ID != e.ID || head.Status == "absent"))
		}
		selected := make([]*memory.Entry, 0, len(entries))
		for _, e := range entries {
			if keep(e) {
				selected = append(selected, e)
			}
		}
		retained := make([]memory.RecallRecord, 0, len(cached))
		for _, record := range cached {
			if keep(record.Entry) {
				retained = append(retained, record)
			}
		}
		r.records, r.text = memory.SelectRecall(selected, retained, r.query, cfg.Recall.TopN, cfg.Recall.MaxChars, cfg.Recall.MinWord, cfg.PromptCatalog)
		if err != nil && r.text != "" {
			r.status = "cached-unavailable"
		}
		r.projection, r.initialized = projection, true
	}
	if r.text != r.published {
		if r.publish != nil {
			if err := r.publish(ctx, r.text); err != nil {
				return "", "", err
			}
		}
		r.published = r.text
	}
	return r.text, r.status, nil
}
