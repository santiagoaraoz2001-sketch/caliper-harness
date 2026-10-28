package tui

import tea "github.com/charmbracelet/bubbletea"

// Fixed routes for existing views. Dialog/composer/history precedence stays in
// handleKey; features retain their typed actions and asynchronous owner guards.
type viewRoute struct {
	render func(*Model, int, int) string
	key    func(Model, tea.KeyMsg) (tea.Model, tea.Cmd, bool)
}

var viewRoutes = map[ViewID]viewRoute{
	ViewChat:      {render: (*Model).renderChat},
	ViewJourney:   {render: (*Model).renderJourney},
	ViewDiff:      {(*Model).renderDiff, Model.diffKey},
	ViewChanges:   {(*Model).renderDiff, Model.diffKey},
	ViewPlans:     {(*Model).renderPlans, Model.plansKey},
	ViewStatus:    {render: (*Model).renderRuntimeStatus},
	ViewManage:    {(*Model).renderServe, Model.serveKey},
	ViewSessions:  {(*Model).renderSessions, Model.sessionsKey},
	ViewDecisions: {(*Model).renderDecisions, Model.decisionsKey},
	ViewWorkflows: {(*Model).renderWorkflows, Model.workflowsKey},
	ViewFiles:     {(*Model).renderFiles, Model.filesKey},
	ViewSettings:  {(*Model).renderSettings, Model.settingsKey},
	ViewTests:     {(*Model).renderTests, Model.testsKey},
	ViewSetup:     {(*Model).renderSetup, Model.setupKey},
}
