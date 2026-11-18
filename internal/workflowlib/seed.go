package workflowlib

// EnsureSeed publishes the first reusable workflow into an empty
// library. It never overwrites user content and never enables the
// workflow; activation stays an explicit user or agent action.
func EnsureSeed(s *Store) error {
	blocks, _ := s.List()
	if len(blocks) > 0 {
		return nil
	}
	type seedBlock struct {
		id, title, purpose, implType, impl, evidence string
	}
	seeds := []seedBlock{
		{"inspect-source", "Inspect source", "Read the responsible source before changing it", "main_model", "Use find_file, grep, and read to inspect the exact source that owns the requested behavior. Record file paths and current behavior.", "inspected file paths and observed current behavior"},
		{"reproduce-issue", "Reproduce issue", "Show the requested problem or missing behavior before the fix", "main_model", "Run the smallest command, test, or manual path that shows the current wrong or missing behavior. Keep the exact command and output.", "reproduction command and its observed output"},
		{"implement-change", "Implement change", "Make the smallest change that owns the requested behavior", "main_model", "Edit the responsible source with edit or write. Keep the change inside the task boundary and preserve unrelated behavior.", "changed file paths from tool receipts"},
		{"run-checks", "Run checks", "Run the project check and the focused behavior check", "tool", "bash", "check command output and exit status"},
	}
	for _, seed := range seeds {
		b, err := s.CreateBlockDraft(seed.id, seed.title)
		if err != nil {
			return err
		}
		implField := "impl_instructions"
		if seed.implType == "tool" {
			implField = "impl_tool"
		}
		fields := []struct{ k, v string }{
			{"purpose", seed.purpose},
			{"inputs", "task request"},
			{"impl_type", seed.implType},
			{implField, seed.impl},
			{"outputs", "recorded result"},
			{"completion_evidence", seed.evidence},
			{"failure_handling", "return to this step with the failed output; do not advance"},
			{"examples", "task request=fix login error => recorded result=observed evidence"},
		}
		for _, f := range fields {
			if b, _, err = s.SetBlockField(seed.id, b.Revision, f.k, f.v); err != nil {
				return err
			}
		}
		if _, err := s.PublishBlock(seed.id, b.Revision); err != nil {
			return err
		}
	}
	w, err := s.CreateWorkflowDraft("repair-and-verify", "Repair and verify")
	if err != nil {
		return err
	}
	for _, f := range []struct{ k, v string }{
		{"purpose", "Repair one reported problem and verify the result"},
		{"inputs", "task request"},
		{"steps", "inspect-source@1\nreproduce-issue@1\nimplement-change@1\nrun-checks@1"},
		{"completion_requirements", "every step advanced with observed evidence; checks pass"},
		{"revision_route", "return to the owning step with the failure evidence; never skip ahead"},
	} {
		if w, _, err = s.SetWorkflowField("repair-and-verify", w.Revision, f.k, f.v); err != nil {
			return err
		}
	}
	_, err = s.PublishWorkflow("repair-and-verify", w.Revision)
	return err
}

type packBlock struct{ id, title, purpose, impl, evidence string }
type packWorkflow struct {
	id, title, purpose, tags, completion string
	blocks                               []packBlock
}

var seedPack = []packWorkflow{
	{"ui-element-design", "UI element design", "Design and build one high-quality UI element with every state a user can reach", "ui-design; frontend", "every step advanced with observed evidence; element checked in every state", []packBlock{
		{"ui-brief", "Write element brief", "State the element purpose, states, and acceptance before building", "Write what the element must do, every state it can be in (default, hover, focus, disabled, loading, empty, error), and how each state will be checked. Keep one owner per fact the element shows.", "brief listing every state and its check"},
		{"ui-build-element", "Build element", "Build the element for every briefed state", "Implement the element so every briefed state renders from real data, never placeholder content. Keep controls reachable by keyboard.", "changed file paths from tool receipts"},
		{"ui-check-states", "Check every state", "Show each state the way a user reaches it", "Drive the element through its real controls into every briefed state and record what rendered in each. A state that only exists in code is not checked.", "per-state observed rendering or test output"},
		{"ui-polish-recheck", "Polish and recheck", "Fix spacing, contrast, and focus defects, then recheck", "Fix defects found in the state check (spacing, contrast, focus order, overflow at small widths), then repeat the state check.", "recheck output with no open defects"},
	}},
	{"oss-ui-search-implement", "Open-source UI search and implement", "Find a proven open-source UI pattern, judge fit and license, then implement an adapted version", "ui-design; research; frontend", "every step advanced with observed evidence; license recorded; adapted code checked in place", []packBlock{
		{"oss-search", "Search open-source UI repositories", "Find candidate implementations of the needed UI pattern", "Search open-source repositories for the exact UI pattern needed. Record each candidate repository, what it does, and its license. Do not copy code in this step.", "candidate list with repository, fit, and license"},
		{"oss-evaluate", "Evaluate candidates", "Pick the candidate to adapt, or decide to build fresh, with reasons", "Compare candidates on fit to the brief, license terms, maintenance state, and dependency weight. Record the decision and what will be adapted versus written fresh.", "decision with reasons for and against each candidate"},
		{"oss-integrate", "Implement adapted pattern", "Implement the chosen pattern in this codebase style", "Implement the adapted pattern following this codebase conventions. Keep required license attribution. Do not paste code the license forbids.", "changed file paths and attribution location"},
		{"oss-verify-fit", "Verify fit and license", "Show the implemented pattern works in place and terms are kept", "Run the element in its real screen, check the states the brief requires, and confirm attribution and license files are present where required.", "in-place check output and license file paths"},
	}},
	{"threejs-asset-animation", "Three.js asset and animation", "Build a Three.js asset or scene and animate it with Anime.js timelines", "3d; threejs; animation", "every step advanced with observed evidence; scene renders and animation plays", []packBlock{
		{"asset-brief", "Write asset brief", "State the asset, camera, motion, and acceptance before building", "Write what the asset or scene must show, the camera view, each planned motion, and how rendering and motion will be checked (screenshot, frame count, or timing).", "brief with view, motions, and checks"},
		{"asset-build", "Build asset or scene", "Build the static asset or scene first", "Build geometry, materials, and lighting for the briefed view before any motion. Keep one owner for scene state.", "changed file paths and a still render check"},
		{"asset-animate", "Animate with timelines", "Add Anime.js timelines for each briefed motion", "Add one timeline per briefed motion with explicit duration and easing. Motions must start, end, and repeat only as briefed; nothing drifts by default.", "timeline definitions and observed start/end behavior"},
		{"asset-verify", "Verify render and motion", "Show the scene renders and every motion plays as briefed", "Render the scene, play each timeline, and record frame behavior against the brief. Fix visual or timing defects at the owning step.", "render evidence and per-motion observed behavior"},
	}},
	{"game-playtest-report", "Game playtest and report", "Play the game as a player and report bugs, failures, and ranked fix recommendations", "playtest; qa; games", "every step advanced with observed evidence; report ranks findings by player impact", []packBlock{
		{"playtest-plan", "Plan playtest", "List the player journeys to test before playing", "List each journey a player takes (start, core loop, progress, failure, recovery, pause/save) and what success looks like for each. Test through shipped controls only.", "journey list with success definition per journey"},
		{"playtest-run", "Play every journey", "Play each journey start to finish as a player", "Play every planned journey using only controls a player has. Record each bug, dead end, wrong message, or confusing moment with the exact steps that caused it.", "per-journey result with reproduction steps for failures"},
		{"playtest-report", "Write ranked report", "Report bugs, failures, and fix recommendations by player impact", "Rank findings by player impact: cannot proceed, wrong result, confusing, cosmetic. For each, give the smallest fix recommendation and the check that would prove it.", "ranked findings each with fix recommendation and proving check"},
	}},
}

// EnsureSeedPack publishes pre-packaged domain skill workflows that are
// missing from the library. Existing blocks and workflows are never
// overwritten, and nothing is auto-enabled.
func EnsureSeedPack(s *Store) error {
	for _, pw := range seedPack {
		if _, ok := s.GetWorkflow(pw.id, 0); ok {
			continue
		}
		var steps []string
		for _, pb := range pw.blocks {
			if _, ok := s.GetBlock(pb.id, 0); !ok {
				b, err := s.CreateBlockDraft(pb.id, pb.title)
				if err != nil {
					return err
				}
				fields := map[string]string{
					"purpose": pb.purpose, "inputs": "task request",
					"impl_type": "main_model", "impl_instructions": pb.impl,
					"outputs": "recorded result", "completion_evidence": pb.evidence,
					"failure_handling": "return to this step with the failed output; do not advance",
					"examples":         "task request=x => recorded result=observed evidence",
				}
				if b, _, err = s.SetBlockFields(pb.id, b.Revision, fields); err != nil {
					return err
				}
				if _, err := s.PublishBlock(pb.id, b.Revision); err != nil {
					return err
				}
			}
			steps = append(steps, pb.id+"@1")
		}
		w, err := s.CreateWorkflowDraft(pw.id, pw.title)
		if err != nil {
			return err
		}
		fields := map[string]string{
			"purpose": pw.purpose, "applies_to": pw.tags, "inputs": "task request",
			"steps": joinSteps(steps), "completion_requirements": pw.completion,
			"revision_route": "return to the owning step with the failure evidence; never skip ahead",
		}
		if w, _, err = s.SetWorkflowFields(pw.id, w.Revision, fields); err != nil {
			return err
		}
		if _, err := s.PublishWorkflow(pw.id, w.Revision); err != nil {
			return err
		}
	}
	return nil
}

func joinSteps(steps []string) string {
	out := ""
	for i, s := range steps {
		if i > 0 {
			out += "\n"
		}
		out += s
	}
	return out
}
