package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"specific-harness/internal/tui/thirdparty/textarea"

	"specific-harness/internal/agent"
	"specific-harness/internal/config"
	"specific-harness/internal/hw"
	"specific-harness/internal/session"
	"specific-harness/internal/tools/ask"
	"specific-harness/internal/tui/slash"
)

// ViewID identifies one of the ten views.
type ViewID string

// The ten views.
const (
	ViewChat      ViewID = "chat"
	ViewJourney   ViewID = "journey"
	ViewPlans     ViewID = "plans"
	ViewSessions  ViewID = "sessions"
	ViewDecisions ViewID = "decisions"
	ViewWorkflows ViewID = "workflows"
	ViewFiles     ViewID = "files"
	ViewDiff      ViewID = "diff"
	ViewChanges   ViewID = "changes"
	ViewSettings  ViewID = "settings"
	ViewStatus    ViewID = "status"
	ViewManage    ViewID = "manage"
	ViewSetup     ViewID = "setup"
)

// View describes a tab-placeable view: its promise plus the task
// building it (loud-missing until then).
type View struct {
	ID    ViewID
	Title string
	Blurb string
	Lands string
}

// Views enumerates the ten views.
var Views = []View{
	{ViewChat, "Chat", "talk to the agent", "T6.2"},
	{ViewJourney, "Workflow", "inspect plan, approach and recorded attempts", "T6.4"},
	{ViewDiff, "Diff", "live file diffs", "T6.3"},
	{ViewChanges, "Changes", "recent observed file history", "T6.3"},
	{ViewPlans, "Plans", "plan/approach/task trees", "T6.4"},
	{ViewSessions, "Sessions", "browse past runs", "T6.6"},
	{ViewDecisions, "Decisions", "decision model activity", "T6.7"},
	{ViewWorkflows, "Workflows", "launches + retries", "T6.8"},
	{ViewFiles, "Files", "browse files", "T6.9"},
	{ViewSettings, "Settings", "edit configuration", "T6.10"},
	{ViewStatus, "Status", "read-only runtime dashboard", "T6.5"},
	{ViewManage, "Models & Servers", "saved connections and runtime controls", "T6.5"},
	{ViewSetup, "Setup", "configure this project", ""},
}

func viewByID(id ViewID) View {
	for _, v := range Views {
		if v.ID == id {
			return v
		}
	}
	return View{ID: id, Title: string(id), Lands: "?"}
}

// place is a navigable position: view plus optional target.
type place struct {
	tab     uint64 // Stable destination when history crosses a retained feature tab.
	view    ViewID
	target  string
	journey journeyFrame
}

// Tab is one retained page with its own back/forward history.
type Tab struct {
	id      uint64
	feature ViewID // Workspace singleton; conversation authority stays in Chat tabs.
	diff    *diffBookmark
	at      place
	dock    dockState
	back    []place
	fwd     []place
	// Title overrides the view name in the tab bar: resumed
	// sessions and the LLM retitler name chats so they scan.
	Title string
	// chat is this tab's own conversation: history, stream, run
	// state. m.Chat mirrors the active tab's chat; switchTab,
	// newTab, and closeTab carry it across on every tab change.
	chat Chat
}

// Alert is one alert-line entry: err, warn, or info.
type Alert struct {
	Kind string
	Text string
}

// Shared crosses the Model value boundary: background goroutines
// reach the program through this one pointer.
type Shared struct {
	Prog *tea.Program
}

// Model is the app: tabs, slash input, completion, alerts, footer.
type Model struct {
	chatTab             uint64
	nextTab             uint64
	chatEditor          *tabEditor
	settingsEditor      *tabEditor
	Tabs                []Tab
	Active              int
	Input               textarea.Model
	Reg                 *slash.Registry
	Alerts              []Alert
	HelpOpen            bool
	HelpScroll          int
	HelpText            string
	HelpNotice          string
	PickerIdx           int
	PickerHide          bool
	St                  Status
	StorePath           string
	Cfg                 config.Config
	Updater             *updateState
	Version             string
	SessionsPath        string
	Server              string
	ModelName           string
	WorkerObservation   workerObservation
	ActivityFrame       uint64
	ActivityTickPending bool
	ReducedMotion       bool
	Chat                Chat
	Comment             []Comment
	Diff                DiffState
	Plans               PlansState
	Journey             journeyState
	Serve               ServeState
	Sessions            SessionsState
	Decisions           DecisionsState
	Wf                  WorkflowsState
	Files               FilesState
	Settings            SettingsState
	Setup               SetupState
	CfgPath             string
	lastTool            string
	lastToolAt          time.Time
	Shared              *Shared
	Width               int
	Height              int
	Hist                []string
	HistIdx             int
	HistDraft           string
	Drafts              [3]string
	Draft               int
	quitPending         bool
}

// New starts one tab on chat with a focused input, footer seeded
// from config (live bits arrive on the first tick).
func New(cfg config.Config, version string) Model {
	ti := newComposer()
	window, _ := hw.ResolveWindow(cfg.Context.Window)
	first := freshChatState()
	m := Model{
		Tabs:    []Tab{{id: 1, at: place{view: ViewChat}, chat: first}},
		chatTab: 1,
		nextTab: 1,
		Input:   ti,
		Reg:     slash.New(),
		Chat:    first,
		Diff:    newDiffState(cfg),
		St: Status{
			Auth:      ShortAuth(cfg.Auth.Mode),
			CtxWindow: window,
			Think:     cfg.Params.Thinking,
			Model:     cfg.Model.Name,
		},
		StorePath:     cfg.Store.Path,
		Cfg:           cfg,
		Version:       version,
		Updater:       &updateState{},
		SessionsPath:  cfg.Sessions.Path,
		Server:        cfg.Model.Server,
		ModelName:     cfg.Model.Name,
		ReducedMotion: os.Getenv("CALIPER_HARNESS_REDUCED_MOTION") == "1" || os.Getenv("SPECIFIC_HARNESS_REDUCED_MOTION") == "1",
		Shared:        &Shared{},
		Width:         80,
		Height:        24,
		HistIdx:       -1,
	}
	m.resizeComposer()
	return m
}

// freshChatState starts an empty conversation. Builders are pointers
// because bubbletea copies the Model by value (see Chat).
func freshChatState() Chat {
	return Chat{ID: session.NewID(), Stream: &strings.Builder{}, FlushedAll: &strings.Builder{}}
}

// restoreChat lands m.Chat on the active tab's conversation. Tabs
// that never held a chat (other views) start fresh.
func (m *Model) restoreChat() {
	if m.Tabs[m.Active].feature != "" {
		m.syncDiffScope()
		m.restoreDiffBookmark()
		m.syncJourneyFocus()
		return
	}
	m.ensureTabID(m.Active)
	m.chatTab = m.Tabs[m.Active].id
	m.Plans.Request++
	m.Journey.Request++
	m.Chat = m.Tabs[m.Active].chat
	if m.Chat.Stream == nil {
		m.Chat = freshChatState()
	}
	m.syncDiffScope()
	m.syncJourneyFocus()
}

// switchTab saves the active tab's chat and lands on next. A run in
// flight refuses: its stream messages route to the active chat, so
// leaving would orphan the run — /stop first.
func (m *Model) switchTab(next int) {
	if !m.canSwitchTab(next) {
		return
	}
	m.saveTabPresentation()
	m.SyncTabChat()
	m.Active = next
	m.restoreChat()
	m.restoreTabEditor()
}

func (m *Model) canSwitchTab(next int) bool {
	if next == m.Active {
		return true
	}
	if m.Chat.Dialog != nil {
		m.alert("info", "answer or dismiss the current dialog before switching tabs")
		return false
	}
	if m.Chat.Running && m.Tabs[next].feature == "" && m.Tabs[next].id != m.chatTab {
		m.alert("info", "a run is in flight on this tab — /stop before switching")
		return false
	}
	return true
}

// newTab keeps the current conversation and opens a fresh one.
func (m *Model) newTab() {
	if m.Chat.Running || m.Chat.Dialog != nil {
		m.alert("info", "a run is in flight on this tab — /stop before opening a new one")
		return
	}
	m.saveTabPresentation()
	m.SyncTabChat()
	m.nextTab++
	m.Tabs = append(m.Tabs, Tab{id: m.nextTab, at: place{view: ViewChat}, chat: freshChatState()})
	m.Active = len(m.Tabs) - 1
	m.restoreChat()
	m.restoreTabEditor()
}

// SyncTabChat stores the working chat back on the active tab. The
// --resume path seeds m.Chat before the program starts, outside the
// tab helpers, so it syncs explicitly after seeding.
func (m *Model) SyncTabChat() {
	if i := m.chatTabIndex(); i >= 0 {
		m.Tabs[i].chat = m.Chat
	}
}

// closeTab drops the active tab and lands on its conversation.
func (m *Model) closeTab() {
	if m.Tabs[m.Active].feature != "" {
		if m.Chat.Dialog != nil {
			m.alert("info", "answer or dismiss the current dialog before closing this tab")
			return
		}
		m.saveTabPresentation()
		m.SyncTabChat()
		m.Tabs = append(m.Tabs[:m.Active], m.Tabs[m.Active+1:]...)
		m.Active = m.chatTabIndex()
		m.restoreTabEditor()
		return
	}
	if m.Chat.Compacting {
		m.alert("info", "stop compaction and wait before closing its tab")
		return
	}
	if m.chatTabCount() <= 1 {
		return
	}
	if m.Chat.Running || m.Chat.Dialog != nil {
		m.alert("info", "a run is in flight on this tab — /stop before closing it")
		return
	}
	m.saveTabPresentation()
	m.Tabs = append(m.Tabs[:m.Active], m.Tabs[m.Active+1:]...)
	for i := len(m.Tabs) - 1; i >= 0; i-- {
		if m.Tabs[i].feature == "" {
			m.Active = i
			break
		}
	}
	m.restoreChat()
	m.restoreTabEditor()
}

// Init blinks the cursor, starts the footer tick, and fetches live
// footer data once immediately.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textarea.Blink, tickStatus(), fetchLive(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes)), m.watchFeed(), m.journeyRead()}
	cmds = append(cmds, m.checkUpdate(false))
	if m.setupActive() {
		cmds = append(cmds, m.probeServe())
	}
	return tea.Batch(cmds...)
}

// suggestions completes the input: command names before the first
// space, the view's context ref after it (plan tree feeds T6.4).
func (m Model) suggestions() []string {
	if m.PickerHide {
		return nil
	}
	v := m.Input.Value()
	if !strings.HasPrefix(v, "/") || strings.Contains(v, "\n") {
		return nil
	}
	return m.Reg.Complete(v, slash.Context{View: string(m.Tabs[m.Active].at.view)})
}

// isExact reports whether the input already names a suggestion.
func isExact(input string, sugs []string) bool {
	for _, s := range sugs {
		if strings.TrimSpace(input) == s {
			return true
		}
	}
	return false
}

// acceptPicker fills the highlighted suggestion into the input.
func (m *Model) acceptPicker(s []string) {
	pick := s[m.PickerIdx%len(s)]
	if head, _, ok := strings.Cut(m.Input.Value(), " "); ok {
		m.Input.SetValue(head + " " + pick)
	} else {
		m.Input.SetValue(pick + " ")
	}
	m.Input.SetCursor(len(m.Input.Value()))
	m.PickerHide = true
	m.PickerIdx = 0
}

func (m *Model) alert(kind, text string) {
	m.Alerts = append(m.Alerts, Alert{kind, text})
	if len(m.Alerts) > m.Cfg.UI.MaxAlerts {
		m.Alerts = m.Alerts[len(m.Alerts)-m.Cfg.UI.MaxAlerts:]
	}
}

func (m *Model) navigate(v ViewID, target string) {
	if v == ViewChat {
		m.focusChat()
		return
	}
	if m.Tabs[m.Active].feature != v {
		m.focusFeature(v, target)
		return
	}
	if target == "" && v != ViewJourney && v != ViewChanges {
		return // A bare command focuses the retained location.
	}
	m.Plans.Request++
	m.Journey.Request++
	t := &m.Tabs[m.Active]
	t.back = append(t.back, m.tabPlace())
	t.fwd = nil
	t.at = place{view: v, target: target}
	if v != ViewJourney {
		t.dock.Focused = false
	}
	m.syncDiffScope()
	m.syncJourneyFocus()
}

func (m *Model) goBack() {
	m.walkTabHistory(false)
}

func (m *Model) goFwd() {
	m.walkTabHistory(true)
}

// submit dispatches one input line: dialog answers, chat messages,
// openMenu turns the input into a bare command so its arg menu shows.
func (m *Model) openMenu(name string) {
	m.Input.SetValue("/" + name + " ")
	m.Input.SetCursor(len(m.Input.Value()))
}

// authChat switches the auth mode: yolo, risky, or all. It takes
// effect on the next run; the in-flight gate is already built.
func (m *Model) authChat(args []string) {
	if len(args) == 0 {
		m.openMenu("auth")
		return
	}
	mode := ""
	switch strings.ToLower(args[0]) {
	case "yolo":
		mode = config.AuthYOLO
	case "risky", "authorize-risky":
		mode = config.AuthRisky
	case "all", "authorize-all":
		mode = config.AuthAll
	default:
		m.alert("err", fmt.Sprintf("unknown auth mode %q — yolo, risky, or all", args[0]))
		return
	}
	m.Cfg.Auth.Mode = mode
	m.St.Auth = ShortAuth(mode)
	m.alert("info", fmt.Sprintf("auth mode: %s (next run)", m.St.Auth))
}

// recordHist files one submitted input and ends history navigation.
func (m *Model) recordHist(input string) {
	if len(m.Hist) == 0 || m.Hist[len(m.Hist)-1] != input {
		m.Hist = append(m.Hist, input)
	}
	m.HistIdx, m.HistDraft = -1, ""
}

// switchDraft saves the live input and swaps to draft n.
func (m *Model) switchDraft(n int) {
	m.Drafts[m.Draft] = m.Input.Value()
	m.Draft = (n + 3) % 3
	m.Input.SetValue(m.Drafts[m.Draft])
	m.Input.SetCursor(len(m.Input.Value()))
	m.HistIdx, m.HistDraft = -1, ""
}

// view slashes, live actions, and loud-missing stubs.
func (m *Model) submit(raw string) tea.Cmd {
	raw = canonicalInput(raw)
	input := strings.TrimSpace(raw)
	command := strings.HasPrefix(input, "/") && !strings.Contains(raw, "\n")
	if input == "" {
		return nil
	}
	if m.askFreeText() && !command {
		m.answerDialog(raw)
		return nil
	}
	// An armed settings edit eats plain text: enter applies, a slash
	// command disarms and runs normally.
	if m.settingsActive() && m.Settings.Editing != nil {
		// Registered slash commands escape an edit; other slash text is a value (e.g. an absolute path).
		if command {
			_, _, command = m.Reg.Parse(input)
		}
		if command {
			m.disarmEdit()
		} else {
			m.applyEdit(input)
			return nil
		}
	}
	if command {
		m.recordHist(input)
	} else {
		m.recordHist(raw)
	}
	if !command {
		if m.Tabs[m.Active].at.view == ViewChat {
			m.startRun(raw)
			return nil
		}
		v := viewByID(m.Tabs[m.Active].at.view)
		m.alert("info", fmt.Sprintf("%s is open — use its keys or /help; /chat returns to chat", v.Title))
		m.Alerts[len(m.Alerts)-1].Text += "; " + m.escapeHint()
		return nil
	}
	c, args, ok := m.Reg.Parse(input)
	if !ok {
		m.alert("err", fmt.Sprintf("unknown command %q — try /help", strings.Fields(input)[0]))
		return nil
	}
	switch {
	case c.Name == "quit":
		return m.requestQuit()
	case c.Name == "help":
		m.HelpOpen = true
		m.HelpScroll = 0
		m.HelpText, m.HelpNotice = "", ""
		return nil
	case c.Name == "manage" && len(args) == 1 && args[0] == "assisted":
		m.openAssistedSetup()
		return nil
	case c.View != "":
		target := ""
		if len(args) > 0 {
			target = args[0]
		}
		m.navigate(ViewID(c.View), target)
		if ViewID(c.View) == ViewDiff {
			m.Diff.History, m.Diff.Filter = false, ""
		}
		if ViewID(c.View) == ViewPlans {
			return m.refreshPlans(target)
		}
		if ViewID(c.View) == ViewStatus {
			return tea.Batch(m.fetchRuntimeMetrics(), m.probeServe())
		}
		if ViewID(c.View) == ViewManage {
			if c.Name == "models" {
				_, exact, _ := m.Reg.Parse(raw)
				if len(exact) > 0 {
					target = exact[0]
				}
			}
			m.Serve.Focus = target
			return m.probeServe()
		}
		if ViewID(c.View) == ViewSessions {
			if target != "" {
				return m.openSession(target)
			}
			return m.refreshSessions()
		}
		if ViewID(c.View) == ViewDecisions {
			return m.refreshDecisions()
		}
		if ViewID(c.View) == ViewWorkflows {
			return m.refreshWorkflows()
		}
		if ViewID(c.View) == ViewFiles {
			return m.refreshFiles()
		}
		return nil
	}
	if len(args) == 0 && len(c.Args) > 0 && c.View == "" && c.Name != "tools" && c.Name != "style" {
		m.openMenu(c.Name)
		return nil
	}
	switch c.Name {
	case "update":
		return m.checkUpdate(true)
	case "setup":
		m.OpenSetup()
		if m.setupActive() {
			return m.probeServe()
		}
	case "new":
		m.newChat()
	case "stop":
		m.stopChat()
	case "retry":
		m.retryChat()
	case "btw":
		m.btwChat(args)
	case "clear":
		m.clearChat()
	case "recall":
		m.recallChat(args)
	case "remember":
		m.rememberChat(args)
	case "style":
		m.styleChat(args)
	case "compact":
		return m.compactChat()
	case "context":
		m.contextChat()
	case "comment":
		m.commentChat(args)
	case "goto":
		m.gotoChat(args)
	case "turns":
		m.turnsChat(args)
	case "changes":
		m.changesChat(args)
	case "open-diff":
		m.openDiffChat(args)
	case "workflow":
		m.workflowChat(args)
	case "tools":
		if m.Chat.Running || m.Wf.Busy != "" || m.compactBusy(false) || m.servingPending() {
			m.alert("err", "switch tools when idle")
			break
		}
		if len(args) == 1 && (args[0] == "coding" || args[0] == "full") {
			m.Chat.Tools = args[0]
			m.Chat.Context.Workflow = nil
			m.Chat.Context.Invalidate()
		} else if len(args) > 0 {
			m.alert("err", "use /tools coding or /tools full")
			break
		}
		view := m.Chat.Tools
		if view == "" {
			view = "coding"
		}
		names := strings.Join(agent.ToolNames(view), ", ")
		if m.Chat.Context.Workflow != nil {
			view, names = "workflow", "selected-node legal operations, refreshed at each request"
		}
		m.Chat.appendLine(lineSys, "tools: "+view+"; active: "+names+". /tools full for planning/memory. Planning: /plans, /workflows; memory: /recall, /remember; GraphQL: caliper-harness graph. Behavior: "+m.Cfg.Behavior.Path)
	case "auth":
		m.authChat(args)
	default:
		m.alert("info", fmt.Sprintf("/%s lands in %s", c.Name, c.Lands))
	}
	return nil
}

// Update routes window events, footer ticks, chat traffic, global
// keys, and input.
func (m Model) Update(msg tea.Msg) (out tea.Model, cmd tea.Cmd) {
	if !m.Chat.readOrigin.Valid {
		m.captureChatAnchor()
	}
	defer func() {
		if next, ok := out.(Model); ok {
			if next.Tabs[next.Active].feature != "" {
				next.SyncTabChat()
			}
			if next.Chat.Context.Workflow != nil {
				next.Chat.Tools = "workflow"
			}
			next.resizeComposer()
			next.syncJourneyFocus()
			next.clampJourneyScroll()
			next.restoreChatAnchor()
			if next.Chat.Dialog != nil && next.Chat.Dialog.Kind == "auth" {
				lines, page := next.authLayout()
				next.Chat.Dialog.Scroll = min(max(0, next.Chat.Dialog.Scroll), max(0, len(lines)-page))
			}
			if next.HelpOpen {
				next.HelpScroll = min(max(0, next.HelpScroll), max(0, len(Wrap(next.helpBody(), next.Width))-max(1, next.Height-len(next.tabChrome())-2)))
			}
			if next.serveActive() {
				lines, _ := next.serveContent(next.Width)
				next.Serve.Scroll = min(max(0, next.Serve.Scroll), max(0, len(lines)-next.servePageHeight()))
			}
			if next.decisionsActive() {
				next.clampDecisions()
			}
			if layout, scroll, avail := next.viewerLayout(next.Tabs[next.Active].at.view); scroll != nil {
				*scroll = layout.clamp(*scroll, avail)
			}
			out = next
			if next.activityAnimating() && !next.ActivityTickPending {
				next.ActivityTickPending = true
				if next.Shared != nil && next.Shared.Prog != nil {
					prog := next.Shared.Prog
					go func() {
						timer := time.NewTimer(100 * time.Millisecond)
						defer timer.Stop()
						<-timer.C
						prog.Send(activityTickMsg{})
					}()
				}
				out = next
			}
		}
	}()
	switch msg := msg.(type) {
	case updateCheckedMsg:
		cmd := m.updateChecked(msg)
		return m, cmd
	case updatePreparedMsg:
		cmd := m.updatePrepared(msg)
		return m, cmd
	case updateFinishedMsg:
		cmd := m.updateFinished(msg)
		return m, cmd
	case activityTickMsg:
		m.ActivityTickPending = false
		if m.activityAnimating() {
			m.ActivityFrame++
		}
		return m, nil
	case tea.MouseMsg:
		return m.mouse(msg)
	case tea.WindowSizeMsg:
		m.Chat.scrollDrag = false
		views := []ViewID{ViewFiles, ViewDiff, ViewPlans}
		anchors := make([]int, len(views))
		for i, v := range views {
			if l, p, _ := m.viewerLayout(v); p != nil {
				anchors[i] = l.topLine(*p)
			}
		}
		m.Width, m.Height = msg.Width, msg.Height
		m.resizeComposer()
		for i, v := range views {
			if l, p, h := m.viewerLayout(v); p != nil {
				*p = l.gotoLine(anchors[i], h)
			}
		}
		return m, nil
	case statusTickMsg:
		cmds := []tea.Cmd{tickStatus(), fetchLive(m.StorePath, uint64(m.Cfg.Store.BufferPoolBytes))}
		if m.Tabs[m.Active].at.view == ViewChat || m.journeyActive() {
			cmds = append(cmds, m.refreshJourney())
		}
		if m.plansActive() {
			cmds = append(cmds, m.refreshPlans(""))
		}
		if m.serveActive() {
			cmds = append(cmds, m.probeServe())
		}
		if m.setupActive() {
			cmds = append(cmds, m.probeServe())
		}
		if m.sessionsActive() {
			cmds = append(cmds, m.refreshSessions())
		}
		if m.decisionsActive() {
			cmds = append(cmds, m.refreshDecisions())
		}
		if m.workflowsActive() {
			cmds = append(cmds, m.refreshWorkflows())
		}
		if m.filesActive() {
			if m.Files.Path != "" && m.Files.Capture == nil {
				cmds = append(cmds, m.readSelectedFile(m.Files.Root, m.Files.Path, nil))
			} else if m.Files.Path == "" {
				cmds = append(cmds, m.refreshFiles())
			}
		}
		return m, tea.Batch(cmds...)
	case runtimeMetricsMsg:
		if chat, _ := m.originChat(msg.Owner); chat != nil && !chat.Running && !chat.Compacting && chat.Seq == msg.Seq {
			chat.Runtime = msg.Metrics
		}
		return m, nil
	case statusLiveMsg:
		if msg.haveGit {
			m.St.Branch, m.St.Dir = msg.branch, msg.dir
		}
		if msg.havePlan {
			m.St.PlanTitle, m.St.PlanApproaches = msg.planTitle, msg.planApproach
		}
		return m, nil
	case showDialogMsg:
		if msg.dismiss {
			if d := m.Chat.Dialog; d != nil && ((msg.answer != nil && d.Answer == msg.answer) || (msg.askDone != nil && d.AskDone == msg.askDone)) {
				m.declineDialog()
			}
			return m, nil
		}
		stale := msg.seq != m.Chat.Seq || m.Chat.stopFlag
		if msg.workflow != "" {
			stale = msg.workflow != m.Wf.Busy || msg.ctx == nil || msg.ctx != m.Wf.ctx
		}
		if stale || (msg.ctx != nil && msg.ctx.Err() != nil) {
			if msg.kind == "auth" {
				msg.answer <- "n"
			} else {
				msg.askDone <- nil
			}
			return m, nil
		}
		m.Chat.Dialog = &Dialog{Workflow: msg.workflow, Kind: msg.kind, Prompt: msg.prompt, Answer: msg.answer, Questions: msg.questions, AskDone: msg.askDone}
		m.syncAskInput(m.askFreeText())
		return m, nil
	case tokenMsg:
		m.onToken(msg)
		return m, nil
	case eventMsg:
		m.onEvent(msg)
		return m, nil
	case diffEventMsg:
		m.onDiffEvent(msg)
		return m, nil
	case journeyLoadedMsg:
		m.onJourneyLoaded(msg)
		return m, nil
	case plansLoadedMsg:
		m.onPlansLoaded(msg)
		return m, nil
	case managedTestMsg:
		m.onManagedTest(msg)
		return m, nil
	case assistedCopiedMsg:
		if m.HelpOpen && m.HelpText != "" {
			m.HelpNotice = "Full prompt copied"
			if msg.err != nil {
				m.HelpNotice = "Clipboard unavailable; select prompt text in your terminal: " + msg.err.Error()
			}
		}
		return m, nil
	case connectionTestMsg:
		if f := m.Setup.Add; f == msg.Form && f.ProbeSeq == msg.Seq && strings.TrimSpace(f.Values[2].Value()) == msg.URL && strings.ToLower(strings.TrimSpace(f.Values[1].Value())) == msg.Preset {
			f.Receipt, f.Catalog = msg.Receipt, msg.Catalog
		}
		return m, nil
	case serveProbedMsg:
		m.onServeProbed(msg)
		return m, nil
	case serveLoadMsg:
		m.onServeLoad(msg)
		if m.quitPending {
			return m, m.requestQuit()
		}
		return m, m.probeServe()
	case sessionsLoadedMsg:
		m.onSessionsLoaded(msg)
		return m, nil
	case decisionsLoadedMsg:
		m.onDecisionsLoaded(msg)
		return m, nil
	case wfLoadedMsg:
		m.onWorkflowsLoaded(msg)
		return m, nil
	case wfTaskSettledMsg:
		if msg.ctx == m.Wf.ctx && msg.approach == m.Wf.Busy {
			m.alert("info", msg.text)
		}
		return m, nil
	case wfDoneMsg:
		return m, m.onWfDone(msg)
	case filesLoadedMsg:
		m.onFilesLoaded(msg)
		return m, nil
	case filesOpenedMsg:
		m.onFilesOpened(msg)
		return m, nil
	case sessionsOpenedMsg:
		m.onSessionsOpened(msg)
		return m, nil
	case runDoneMsg:
		return m, m.onRunDone(msg)
	case titleMsg:
		m.onTitle(msg)
		return m, nil
	case compactDoneMsg:
		cmd := m.onCompactDone(msg)
		if cmd != nil {
			return m, cmd
		}
		if m.quitPending {
			return m, m.requestQuit()
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	m.Input, cmd = m.Input.Update(msg)
	return m, cmd
}

// Keys: ctrl+t new tab, ctrl+n/ctrl+p cycle, ctrl+w close,
// ctrl+o/ctrl+g back/forward, pgup/pgdn scroll chat, tab accepts
// askOwns reports whether the option dialog consumes this key.
// Draft typing still reaches the input; the dialog owns only its
// own navigation.
func (m Model) askOwns(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyUp, tea.KeyDown, tea.KeyTab, tea.KeyEnter, tea.KeyEsc:
		return true
	}
	if m.Chat.Dialog.Commenting {
		switch msg.Type {
		case tea.KeyRunes, tea.KeySpace, tea.KeyBackspace, tea.KeyCtrlJ:
			return true
		}
	}
	return false
}

// askKey drives the option screen: arrows pick, tab comments,
// enter sends, esc exits the comment or declines.
func (m Model) askKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.Chat.Dialog
	if msg.Type == tea.KeyEsc {
		if d.Commenting {
			d.Commenting = false
			return m, nil
		}
		m.declineDialog()
		return m, nil
	}
	if d.Commenting && msg.Type != tea.KeyUp && msg.Type != tea.KeyDown {
		switch msg.Type {
		case tea.KeyEnter:
			d.Commenting = false
			m.advanceAsk(ask.Answer{Picked: d.Questions[d.QIdx].Options[d.PickIdx], Comment: d.Comment})
			return m, nil
		case tea.KeyTab:
			d.Commenting = false
			return m, nil
		case tea.KeyBackspace:
			if r := []rune(d.Comment); len(r) > 0 {
				d.Comment = string(r[:len(r)-1])
			}
			return m, nil
		case tea.KeyRunes, tea.KeySpace, tea.KeyCtrlJ:
			text := string(msg.Runes)
			if msg.Type == tea.KeySpace {
				text = " "
			}
			if msg.Type == tea.KeyCtrlJ {
				text = "\n"
			}
			comment := d.Comment + canonicalInput(text)
			if strings.Count(comment, "\n") >= textarea.LineLimit {
				m.alert("err", fmt.Sprintf("input exceeds %d lines; comment unchanged", textarea.LineLimit))
			} else {
				d.Comment = comment
			}
			return m, nil
		default:
			return m, nil
		}
	}
	switch msg.Type {
	case tea.KeyUp:
		n := len(d.Questions[d.QIdx].Options)
		d.PickIdx = (d.PickIdx - 1 + n) % n
	case tea.KeyDown:
		n := len(d.Questions[d.QIdx].Options)
		d.PickIdx = (d.PickIdx + 1) % n
	case tea.KeyTab:
		d.Commenting = true
	case tea.KeyEnter:
		m.advanceAsk(ask.Answer{Picked: d.Questions[d.QIdx].Options[d.PickIdx], Comment: d.Comment})
	}
	return m, nil
}

// escapeHint describes the next Escape through the existing key-owner order.
func (m Model) escapeHint() string {
	switch {
	case m.HelpOpen:
		return "esc closes help"
	case m.askOptions() && m.Chat.Dialog.Commenting:
		return "esc leaves comment"
	case m.Chat.Dialog != nil:
		if m.Chat.Dialog.Kind == "auth" {
			return "esc declines"
		}
		return "esc skips questions"
	case m.diffActive() && m.Diff.Viewer != nil, m.plansActive() && m.Plans.Detail != nil, m.sessionsActive() && m.Sessions.Detail != nil, m.filesActive() && m.Files.Path != "":
		return "esc closes detail"
	case m.settingsActive() && m.Settings.Editing != nil:
		return "esc cancels edit"
	case m.setupActive() && m.Setup.Stage > 0:
		return "esc previous setup step"
	case len(m.suggestions()) > 0:
		return "esc closes menu"
	case m.journeyActive():
		return "esc back one layer"
	case m.Tabs[m.Active].dock.Focused && m.Tabs[m.Active].at.view == ViewChat:
		return "esc input"
	case m.Chat.Running:
		return "esc stops run"
	case len(m.Alerts) > 0:
		return "esc dismisses alert"
	case m.Tabs[m.Active].at.view != ViewChat:
		return "esc chat"
	default:
		return "esc no pending action"
	}
}

// completion, esc walks back (dialog, picker, run, alert).
// y/n answers auth dialogs; shift+up/down browses history;
// shift+left/right swaps drafts. Bare arrows edit text (picker excepted).
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.HelpOpen {
		if m.HelpText != "" && msg.String() == "c" {
			return m, m.copyAssistedSetup()
		}
		page := max(1, m.Height-len(m.tabChrome())-2)
		switch msg.Type {
		case tea.KeyUp:
			m.HelpScroll--
		case tea.KeyDown:
			m.HelpScroll++
		case tea.KeyPgUp:
			m.HelpScroll -= page
		case tea.KeyPgDown:
			m.HelpScroll += page
		case tea.KeyHome:
			m.HelpScroll = 0
		case tea.KeyEnd:
			m.HelpScroll = len(Wrap(m.helpBody(), m.Width))
		case tea.KeyCtrlC:
			return m, m.requestQuit()
		default:
			m.HelpOpen = false
		}
		return m, nil
	}
	if (m.setupActive() || m.serveActive()) && m.Setup.Add != nil && m.Chat.Dialog == nil && m.Input.Value() == "" && msg.Type != tea.KeyEsc && msg.Type != tea.KeyCtrlC {
		um, cmd, _ := m.connectionKey(msg)
		return um, cmd
	}
	if m.askOptions() && m.askOwns(msg) {
		return m.askKey(msg)
	}
	switch msg.Type {
	case tea.KeyCtrlC:
		return m, m.requestQuit()
	case tea.KeyEsc:
		if m.serveActive() && m.Setup.Add != nil {
			m.Setup.Add = nil
			return m, nil
		}
		if m.Chat.Dialog != nil {
			m.declineDialog()
			return m, nil
		}
		if m.setupActive() {
			if m.Setup.Add != nil {
				m.Setup.Add = nil
				return m, nil
			}
			if m.Setup.Stage > 0 {
				m.Setup.Stage--
				m.Setup.Err = ""
			} else {
				m.focusChat()
			}
			return m, nil
		}
		if m.diffActive() && m.Diff.Viewer != nil {
			m.Diff.Viewer = nil
			return m, nil
		}
		if m.plansActive() && m.Plans.Detail != nil {
			m.Plans.Detail = nil
			return m, nil
		}
		if m.sessionsActive() && m.Sessions.Detail != nil {
			m.Sessions.Detail = nil
			return m, nil
		}
		if m.filesActive() && m.Files.Path != "" {
			m.Files.Path, m.Files.Err = "", ""
			m.Files.Ticket++
			m.Files.Viewer = nil
			m.Files.Capture = nil
			return m, nil
		}
		if m.settingsActive() && m.Settings.Editing != nil {
			m.disarmEdit()
			m.alert("info", "edit cancelled")
			return m, nil
		}
		if len(m.suggestions()) > 0 {
			m.PickerHide = true
			return m, nil
		}
		if m.journeyActive() {
			m.goBack()
			return m, nil
		}
		if m.Tabs[m.Active].dock.Focused && m.Tabs[m.Active].at.view == ViewChat {
			m.Tabs[m.Active].dock.Focused = false
			m.Input.Focus()
			return m, nil
		}
		if m.Chat.Running {
			m.stopChat()
			return m, nil
		}
		if len(m.Alerts) > 0 {
			m.Alerts = m.Alerts[:len(m.Alerts)-1]
			return m, nil
		}
		if m.diffActive() || m.plansActive() || m.serveActive() || m.Tabs[m.Active].at.view == ViewStatus || m.sessionsActive() || m.decisionsActive() || m.workflowsActive() || m.filesActive() || m.settingsActive() {
			m.showChat()
			return m, nil
		}
		return m, nil
	}
	if m.Chat.Dialog != nil && m.Chat.Dialog.Kind == "auth" {
		lines, page := m.authLayout()
		navigated := true
		switch msg.Type {
		case tea.KeyUp:
			m.Chat.Dialog.Scroll--
		case tea.KeyDown:
			m.Chat.Dialog.Scroll++
		case tea.KeyPgUp:
			m.Chat.Dialog.Scroll -= page
		case tea.KeyPgDown:
			m.Chat.Dialog.Scroll += page
		case tea.KeyHome:
			m.Chat.Dialog.Scroll = 0
		case tea.KeyEnd:
			m.Chat.Dialog.Scroll = len(lines)
		default:
			navigated = false
		}
		if navigated {
			return m, nil
		}
		switch msg.String() {
		case "y", "Y":
			m.answerDialog("y")
			return m, nil
		case "n", "N":
			m.answerDialog("n")
			return m, nil
		}
	}
	if !msg.Alt {
		switch msg.Type {
		case tea.KeyCtrlN:
			m.switchTab((m.Active + 1) % len(m.Tabs))
			return m, nil
		case tea.KeyCtrlP:
			m.switchTab((m.Active - 1 + len(m.Tabs)) % len(m.Tabs))
			return m, nil
		case tea.KeyCtrlT:
			m.newTab()
			return m, nil
		case tea.KeyCtrlW:
			m.closeTab()
			return m, nil
		case tea.KeyCtrlO:
			m.goBack()
			return m, nil
		case tea.KeyCtrlG:
			m.goFwd()
			return m, nil
		case tea.KeyPgUp:
			if m.serveActive() && m.Input.Value() == "" {
				m.Serve.Scroll -= m.servePageHeight()
				return m, nil
			}
			if m.Tabs[m.Active].at.view == ViewChat {
				m.pageChat(-1)
				return m, nil
			}
		case tea.KeyPgDown:
			if m.serveActive() && m.Input.Value() == "" {
				m.Serve.Scroll += m.servePageHeight()
				return m, nil
			}
			if m.Tabs[m.Active].at.view == ViewChat {
				m.pageChat(1)
				return m, nil
			}
		}
	}
	if msg.Type == tea.KeyRunes || msg.Type == tea.KeyBackspace || msg.Type == tea.KeySpace {
		m.PickerHide = false
		m.PickerIdx = 0
	}
	if (msg.Type == tea.KeyUp || msg.Type == tea.KeyDown) && len(m.suggestions()) > 0 {
		n := len(m.suggestions())
		if msg.Type == tea.KeyUp {
			m.PickerIdx = (m.PickerIdx - 1 + n) % n
		} else {
			m.PickerIdx = (m.PickerIdx + 1) % n
		}
		return m, nil
	}
	if next, cmd, done := m.journeyKey(msg); done {
		return next, cmd
	} else {
		m = next.(Model)
	}
	if m.askFreeText() && m.Chat.Dialog.FreeDraft != nil {
		switch msg.Type {
		case tea.KeyShiftUp, tea.KeyShiftDown, tea.KeyShiftLeft, tea.KeyShiftRight:
			m.alert("info", "answer or cancel this question before switching drafts or history")
			return m, nil
		}
	}
	if msg.Type == tea.KeyShiftUp || msg.Type == tea.KeyShiftDown {
		if len(m.Hist) > 0 {
			switch {
			case m.HistIdx == -1 && msg.Type == tea.KeyShiftUp:
				m.HistDraft = m.Input.Value()
				m.HistIdx = len(m.Hist) - 1
			case m.HistIdx == -1:
				// down at live input: nothing newer
			case msg.Type == tea.KeyShiftUp && m.HistIdx > 0:
				m.HistIdx--
			case msg.Type == tea.KeyShiftDown:
				m.HistIdx++
				if m.HistIdx >= len(m.Hist) {
					m.HistIdx = -1
					m.Input.SetValue(m.HistDraft)
					m.Input.SetCursor(len(m.Input.Value()))
					return m, nil
				}
			}
			if m.HistIdx != -1 {
				m.Input.SetValue(m.Hist[m.HistIdx])
				m.Input.SetCursor(len(m.Input.Value()))
			}
		}
		return m, nil
	}
	if msg.Type == tea.KeyShiftLeft || msg.Type == tea.KeyShiftRight {
		if msg.Type == tea.KeyShiftLeft {
			m.switchDraft(m.Draft - 1)
		} else {
			m.switchDraft(m.Draft + 1)
		}
		return m, nil
	}
	if m.setupActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.setupKey(msg); done {
			return um, cmd
		}
	}
	if m.diffActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.diffKey(msg); done {
			return um, cmd
		}
	}
	if m.plansActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.plansKey(msg); done {
			return um, cmd
		}
	}
	if m.serveActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.serveKey(msg); done {
			return um, cmd
		}
	}
	if m.sessionsActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.sessionsKey(msg); done {
			return um, cmd
		}
	}
	if m.decisionsActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.decisionsKey(msg); done {
			return um, cmd
		}
	}
	if m.workflowsActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.workflowsKey(msg); done {
			return um, cmd
		}
	}
	if m.filesActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.filesKey(msg); done {
			return um, cmd
		}
	}
	if m.settingsActive() && len(m.suggestions()) == 0 {
		if um, cmd, done := m.settingsKey(msg); done {
			return um, cmd
		}
	}
	switch msg.Type {
	case tea.KeyEnter:
		if s := m.suggestions(); len(s) > 0 && !isExact(m.Input.Value(), s) {
			m.acceptPicker(s)
			return m, nil
		}
		v := m.Input.Value()
		m.Input.Reset()
		m.PickerIdx = 0
		m.PickerHide = false
		return m, m.submit(v)
	case tea.KeyTab:
		if s := m.suggestions(); len(s) > 0 {
			m.acceptPicker(s)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.Input, cmd = m.Input.Update(msg)
	return m, cmd
}

// keyHelp lists keyboard commands; /help shows it under the slashes.
func keyHelp() string {
	rows := []struct{ area, keys string }{
		{"input", "Enter send · Ctrl+J newline · → marks tabs · shift+↑↓ history · shift+←→ drafts"},
		{"chat", "pgup/pgdn scroll"},
		{"diff", "↑↓ select · enter open · f comment · esc chat"},
		{"plans", "↑↓ move · →← expand · enter dive · f comment · r refresh · esc chat"},
		{"status", "read-only runtime metrics · /manage connections · esc chat"},
		{"manage", "n Add server · + Add model · g LLM-assisted setup · e Edit · backspace Remove · t Test · u Coding · j Judge · s start · x unload · l load"},
		{"sessions", "↑↓ select · enter open · r refresh · esc chat"},
		{"slash menu", "↑↓ move · enter complete · esc close"},
		{"dialogs", "y/n answer · esc dismiss"},
		{"ask", "↑↓ pick · tab comment · enter answer · esc skip all"},
		{"tabs", "ctrl+t new tab · ctrl+n/p switch tab · ctrl+w close tab · ctrl+o/g tab back/fwd"},
	}
	var b strings.Builder
	b.WriteString("KEYS\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-10s %s\n", r.area, r.keys)
	}
	return b.String()
}

// View renders tabs, view, alert, picker, chips, dialog, input, footer.
func (m Model) View() string {
	if m.Chat.Context.Workflow != nil {
		m.Chat.Tools = "workflow"
	}
	if m.HelpOpen {
		lines := Wrap(m.helpBody(), m.Width)
		start := min(max(0, m.HelpScroll), max(0, len(lines)-max(1, m.Height-len(m.tabChrome())-2)))
		end := min(len(lines), start+max(1, m.Height-len(m.tabChrome())-2))
		return strings.Join(m.tabChrome(), "\n") + "\n" + ansi.Truncate(Title.Render(fmt.Sprintf("%s · %d-%d/%d", m.helpHeading(), start+1, end, len(lines))), m.Width, "…") + "\n" + strings.Join(lines[start:end], "\n") + "\n" + ansi.Truncate(Muted.Render(m.helpHint()), m.Width, "…")
	}
	if m.Chat.Dialog != nil && m.Chat.Dialog.Kind == "auth" {
		return strings.Join(m.tabChrome(), "\n") + "\n" + m.authView()
	}
	var b strings.Builder
	b.WriteString(strings.Join(m.tabChrome(), "\n") + "\n")
	vh := viewHeight(m)
	if m.Tabs[m.Active].at.view == ViewChat {
		b.WriteString(m.renderChat(m.Width, vh) + "\n")
	} else if m.journeyActive() {
		b.WriteString(m.renderJourney(m.Width, vh) + "\n")
	} else if m.diffActive() {
		b.WriteString(m.renderDiff(m.Width, vh) + "\n")
	} else if m.Tabs[m.Active].at.view == ViewPlans {
		b.WriteString(m.renderPlans(m.Width, vh) + "\n")
	} else if m.Tabs[m.Active].at.view == ViewStatus {
		b.WriteString(m.renderRuntimeStatus(m.Width, vh) + "\n")
	} else if m.Tabs[m.Active].at.view == ViewManage {
		b.WriteString(m.renderServe(m.Width, vh) + "\n")
	} else if m.Tabs[m.Active].at.view == ViewSessions {
		b.WriteString(m.renderSessions(m.Width, vh) + "\n")
	} else if m.Tabs[m.Active].at.view == ViewDecisions {
		b.WriteString(m.renderDecisions(m.Width, vh) + "\n")
	} else if m.Tabs[m.Active].at.view == ViewWorkflows {
		b.WriteString(m.renderWorkflows(m.Width, vh) + "\n")
	} else if m.Tabs[m.Active].at.view == ViewFiles {
		b.WriteString(m.renderFiles(m.Width, vh) + "\n")
	} else if m.Tabs[m.Active].at.view == ViewSettings {
		b.WriteString(m.renderSettings(m.Width, vh) + "\n")
	} else if m.setupActive() {
		b.WriteString(m.renderSetup(m.Width, vh) + "\n")
	} else {
		lines := strings.Split(placeholderView(m.Tabs[m.Active]), "\n")
		for len(lines) < vh {
			lines = append(lines, "")
		}
		b.WriteString(strings.Join(lines, "\n") + "\n")
	}
	if len(m.Alerts) > 0 {
		last := m.Alerts[len(m.Alerts)-1]
		var s string
		switch last.Kind {
		case "err":
			s = ErrSt.Render("! " + last.Text)
		case "warn":
			s = WarnSt.Render("! " + last.Text)
		default:
			s = InfoSt.Render("i " + last.Text)
		}
		if len(m.Alerts) > 1 {
			s += Muted.Render(fmt.Sprintf(" (+%d more)", len(m.Alerts)-1))
		}
		if m.serveActive() {
			s = ansi.Truncate(s, m.Width, "…")
		}
		b.WriteString(s + "\n")
	}
	if s := m.suggestions(); len(s) > 0 {
		sel := m.PickerIdx % len(s)
		page := sel / 8
		b.WriteString(FaintSt.Render(fmt.Sprintf("  %s · %d/%d · ↑↓ select · tab accept", m.Input.Value(), sel+1, len(s))) + "\n")
		start, end := page*8, page*8+8
		if end > len(s) {
			end = len(s)
		}
		show, more := s[start:end], len(s)-end
		if start > 0 {
			b.WriteString(FaintSt.Render(fmt.Sprintf("  …%d above", start)) + "\n")
		}
		for i, sug := range show {
			branch := "├─"
			if i == len(show)-1 && more == 0 {
				branch = "└─"
			}
			tail := ""
			if c, _, ok := m.Reg.Parse(sug); ok {
				tail = " — " + truncate(c.Desc, 40)
			}
			head := "  " + branch + " " + sug
			if start+i == sel {
				b.WriteString(PickerSel.Render(head) + Guidance.Render(tail) + "\n")
			} else {
				b.WriteString(Guidance.Render("  "+branch+" ") + Guidance.Render(sug) + Guidance.Render(tail) + "\n")
			}
		}
		if more > 0 {
			b.WriteString(FaintSt.Render(fmt.Sprintf("  …%d more", more)) + "\n")
		}
		if usage := m.pickerUsage(s[sel]); usage != "" {
			b.WriteString(Guidance.Render(truncate("  Syntax: "+usage, m.Width)) + "\n")
		}
	}
	if chips := chipsView(m); chips != "" {
		b.WriteString(chips + "\n")
	}
	if dialog := dialogView(m); dialog != "" {
		b.WriteString(dialog + "\n")
	}
	left := fmt.Sprintf("Enter send · Ctrl+J newline · → marks tabs · draft %d/3", m.Draft+1)
	if m.Tabs[m.Active].at.view != ViewChat && !m.askFreeText() {
		left = "Enter /command · " + m.escapeHint()
		if m.settingsActive() && m.Settings.Editing != nil {
			left = "Enter applies edit · " + m.escapeHint()
		}
	}
	if m.Tabs[m.Active].at.view == ViewChat && m.Chat.Running {
		left = "Enter /command · Ctrl+J newline · running · " + m.escapeHint() + " · /btw"
	}
	inputView := m.composerView()
	if m.setupActive() && m.Chat.Dialog == nil && m.Setup.Stage == 2 && m.Setup.CheckFocused && m.Input.Value() == "" {
		left = "Enter review · Tab /command · " + m.escapeHint()
		ti := m.Input
		ti.Blur()
		inputView = ti.View()
	}
	if m.setupActive() && m.Chat.Dialog == nil && m.Input.Value() == "" {
		if m.Setup.Stage == 0 {
			left = "↑↓ select · Enter continue · r rescan · esc chat"
		}
		if m.Setup.Stage == 3 {
			left = "Enter save · PgUp/PgDn review · " + m.escapeHint()
		}
	}
	if m.Tabs[m.Active].dock.Focused && m.Tabs[m.Active].at.view == ViewChat && m.Chat.Dialog == nil {
		left = "↑↓ workflow selection · enter open · esc input"
	}
	if m.Chat.Dialog != nil {
		left = m.ownedHint("dialog open")
		if !m.askFreeText() {
			ti := m.Input
			ti.Blur()
			inputView = ti.View()
		}
	}
	rightHints := "⇧←→ drafts · ⇧↑↓ history"
	if m.Chat.Dialog != nil {
		rightHints = ""
	}
	if m.Tabs[m.Active].at.view == ViewChat {
		b.WriteString("\n")
	}
	b.WriteString(composerBox(left, rightHints, inputView, m.Width) + "\n")
	footer := m.footer()
	if dock := m.dockRows(); len(dock) > 0 {
		b.WriteString(strings.Join(dock, "\n") + "\n")
		rows := strings.Split(footer, "\n")
		footer = rows[0] + "\n" + rows[2]
	}
	if m.serveActive() {
		lines := strings.Split(footer, "\n")
		for i := range lines {
			lines[i] = ansi.Truncate(lines[i], m.Width, "…")
		}
		footer = strings.Join(lines, "\n")
	}
	b.WriteString(footer)
	frame := strings.Split(b.String(), "\n")
	for i := range frame {
		frame[i] = truncate(frame[i], m.Width)
	}
	return strings.Join(frame, "\n")
}

// viewHeight splits the frame: tabs, rule, view, alert?, picker?,
// chips?, dialog?, actual input height + borders, footer (3).
func viewHeight(m Model) int {
	vh := m.Height - len(m.tabChrome()) - (m.Input.Height() + 2) - 3 - m.dockExtraHeight()
	if len(m.Alerts) > 0 {
		vh--
	}
	if m.Tabs[m.Active].at.view == ViewChat {
		vh--
	}
	vh -= pickerLines(m)
	if chipsView(m) != "" {
		vh--
	}
	if d := dialogView(m); d != "" {
		vh -= strings.Count(d, "\n") + 1
	}
	if vh < 1 {
		vh = 1
	}
	return vh
}

// chipsView shows pending comments and recall hits (chat only).
func chipsView(m Model) string {
	if m.Tabs[m.Active].at.view != ViewChat {
		return ""
	}
	var parts []string
	if n := len(m.Comment); n > 0 {
		refs := make([]string, 0, min(n, 2))
		for _, c := range m.Comment[:min(n, 2)] {
			ref := c.Ref
			if ref != "" && (c.Source == "file" || c.Source == string(ViewFiles) || c.Source == string(ViewDiff) || c.Source == string(ViewChanges)) {
				ref = filepath.Base(ref)
			}
			if ref == "" {
				ref = truncate(oneLine(c.Text), 28)
			}
			if c.Lines != "" {
				ref += " " + c.Lines
			}
			refs = append(refs, ref)
		}
		text := fmt.Sprintf("%d comment pending", n)
		if n != 1 {
			text = fmt.Sprintf("%d comments pending", n)
		}
		text += " · " + strings.Join(refs, " · ")
		if n > 2 {
			text += fmt.Sprintf(" · +%d", n-2)
		}
		parts = append(parts, Guidance.Render(truncate(text, m.Width)))
	}
	if m.Chat.Recalled > 0 {
		parts = append(parts, Muted.Render(fmt.Sprintf("recalled %d", m.Chat.Recalled)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, FaintSt.Render(" · "))
}

const authHint = "↑↓ scroll · pgup/pgdn · home/end · y approve · n deny · esc declines"

func (m Model) authLayout() ([]string, int) {
	return approvalRows(m.Chat.Dialog.Prompt, BoxInner(m.Width)), max(1, m.Height-m.Input.Height()-len(Wrap(authHint, BoxInner(m.Width)))-4-len(m.tabChrome()))
}

func (m Model) authView() string {
	lines, page := m.authLayout()
	start := min(max(0, m.Chat.Dialog.Scroll), max(0, len(lines)-page))
	end := min(len(lines), start+page)
	title := fmt.Sprintf("Auth · approval · %d-%d/%d", start+1, end, len(lines))
	input := m.Input
	input.Blur()
	return Box(title, strings.Join(lines[start:end], "\n")+"\n"+Guidance.Render(strings.Join(Wrap(authHint, BoxInner(m.Width)), "\n")), m.Width) + "\n" + composerBox("dialog open · Enter /stop · Ctrl+C cancels", "", input.View(), m.Width)
}

// dialogView boxes the active modal (any view: it demands attention).
func dialogView(m Model) string {
	d := m.Chat.Dialog
	if d == nil {
		return ""
	}
	if m.askOptions() {
		return askOptionsView(m, d)
	}
	title, hint, prompt := "Auth", "y approve · n deny · esc declines", d.Prompt
	if d.Kind == "ask" {
		title, hint = "Question", "type answer · enter sends · esc cancels"
		if d.QIdx >= 0 && d.QIdx < len(d.Questions) {
			title = fmt.Sprintf("Question %d/%d", d.QIdx+1, len(d.Questions))
			prompt = d.Questions[d.QIdx].Text
		}
	}
	return Box(title, prompt+"\n"+Guidance.Render(hint), m.Width)
}

// askCommentLine renders the greyed comment row under the current
// pick; it brightens with a cursor while commenting.
func askCommentLine(d *Dialog, inner int) string {
	text := truncate("  └─ comment: "+d.Comment, inner)
	if d.Commenting {
		return Body.Render(text + "▍")
	}
	return Muted.Render(text)
}

// askOptionsView renders one chained question with its picks.
func askOptionsView(m Model, d *Dialog) string {
	inner := m.Width - 4
	if inner < 10 {
		inner = 10
	}
	q := d.Questions[d.QIdx]
	lines := Wrap(q.Text, inner)
	for i, o := range q.Options {
		mark := "  "
		if i == d.PickIdx {
			mark = "❯ "
		}
		lines = append(lines, truncate(mark+o, inner))
		if i == d.PickIdx {
			lines = append(lines, askCommentLine(d, inner))
		}
	}
	title := fmt.Sprintf("Question %d/%d", d.QIdx+1, len(d.Questions))
	return Box(title, strings.Join(lines, "\n")+"\n"+Guidance.Render("↑↓ pick · tab comment · enter send · "+m.escapeHint()), m.Width)
}

func titleOf(id ViewID) string {
	return viewByID(id).Title
}

func (m Model) pickerUsage(suggestion string) string {
	candidate := suggestion
	if head, _, ok := strings.Cut(strings.TrimLeft(m.Input.Value(), " "), " "); ok {
		candidate = head
	}
	if c, _, ok := m.Reg.Parse(candidate); ok && c.Usage != candidate {
		return c.Usage
	}
	return ""
}
func pickerLines(m Model) int {
	sugs := m.suggestions()
	n := len(sugs)
	if n == 0 {
		return 0
	}
	sel := m.PickerIdx % n
	start := sel / 8 * 8
	end := min(n, start+8)
	rows := 1 + end - start
	if start > 0 {
		rows++
	}
	if end < n {
		rows++
	}
	if m.pickerUsage(sugs[sel]) != "" {
		rows++
	}
	return rows
}

// placeholderView stands in until each view's task lands it.
func placeholderView(t Tab) string {
	v := viewByID(t.at.view)
	var b strings.Builder
	b.WriteString(Title.Render(v.Title) + "\n")
	b.WriteString(Muted.Render(v.Blurb) + "\n")
	if t.at.target != "" {
		b.WriteString(Muted.Render("target: "+t.at.target) + "\n")
	}
	b.WriteString("\n" + Muted.Render("Lands in "+v.Lands))
	return b.String()
}

// requestQuit keeps the UI alive until owned work has released its leases.
func (m *Model) requestQuit() tea.Cmd {
	m.quitPending = true
	if m.Updater != nil && m.Updater.cancel != nil {
		m.Updater.cancel()
	}
	if m.Updater != nil && m.Updater.busy {
		return nil
	}
	if m.Chat.Running {
		m.stopChat()
	}
	if m.Wf.cancel != nil {
		if m.Chat.Dialog != nil && m.Chat.Dialog.Workflow == m.Wf.Busy {
			m.declineDialog()
		}
		m.Wf.cancel()
	}
	if m.Serve.cancel != nil {
		m.Serve.cancel()
	}
	if m.Serve.Loading != "" {
		return nil
	}
	if m.compactBusy(true) || m.Chat.Running || m.Wf.Busy != "" {
		return nil
	}
	return tea.Quit
}
