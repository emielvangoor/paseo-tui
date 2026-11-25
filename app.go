package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const historyTail = 400

type focus int

const (
	focusList focus = iota
	focusInput
)

type overlay int

const (
	ovNone overlay = iota
	ovHelp
	ovConfirm
	ovNew
	ovPalette
	ovSubs
	ovSettings
	ovMenu
	ovProject
	ovRename
	ovPane
	ovQuestion
	ovHistory
)

// ---------- messages ----------

type bridgeBatchMsg struct {
	br   *Bridge
	msgs []BridgeMsg
}
type bridgeClosedMsg struct{ br *Bridge }
type bridgeRestartMsg struct{}
type actionDoneMsg struct {
	what    string
	err     error
	agentID string // set when an action produced an agent to select
}
type providersMsg struct {
	provs []Provider
	err   error
}

// ---------- model ----------

type BridgeConfig struct {
	Node, Script string
	Args         []string
	Env          []string // e.g. ELECTRON_RUN_AS_NODE when using Paseo's runtime
	HostLabel    string
}

// turnProgress is live progress of the watched agent, fed by stream events.
type turnProgress struct {
	started time.Time
	usage   *Usage
}

type Model struct {
	cfg BridgeConfig
	br  *Bridge

	updateAvail string // a newer release, from the background update check
	updateHint  string // how to install it for this kind of install

	width, height int
	focus         focus
	overlay       overlay

	// connection
	local     bool
	connected bool
	down      bool
	downCode  string
	downMsg   string
	connState string

	agents     []Agent
	perms      map[string][]PermissionReq
	selectedID string
	streamID   string
	showAll    bool
	loaded     bool

	timeline *Timeline
	render   *Renderer
	vp       viewport.Model
	follow   bool
	progress map[string]*turnProgress

	input   textarea.Model
	spin    spinner.Model
	status  string
	statusT time.Time
	isErr   bool

	confirmText string
	confirmFn   func() tea.Cmd
	askingQuit  bool // the confirmation is the quit question (q or ctrl+c again confirms)

	form      *NewAgentForm
	providers []Provider
	palette   *Palette

	// sidebar: projects -> workspaces; agents are tabs of a workspace
	workspaces    []Workspace
	emptyProjects []EmptyProject
	wsKnown       bool // daemon streams workspaces
	wsViews       map[string]*wsView
	projects      []*projView
	pinned        []*wsView
	selWS         string
	title         string            // last terminal title sent
	lastTab       map[string]string // workspace -> last focused agent tab
	cursor        int
	rows          []row
	state         *uiState
	hostKey       string

	attachments []string // image files to send with the next message

	// subagents
	subs    map[string]map[string]*ProviderSub // parent agent -> provider subagents
	subTabs map[string][]tabItem               // workspace -> opened subagent tabs
	selSub  *subRef                            // focused provider-subagent tab (selectedID is its parent)
	subList *SubList

	sel        selection
	sideScroll int // extra sidebar scroll from the mouse wheel
	lastWheel  time.Time
	wheelSpeed int
	composerH  int // current height of the message box

	settings       *Settings
	menu           *ActionMenu
	projForm       *ProjectForm
	renaming       *renameForm
	panes          map[paneID]*termPane // floating terminal panes (e editor, F files) per workspace, also while hidden
	question       *questionForm        // the open answer form of an agent's question
	history        *historyList         // History (H): all agents, archived ones included
	paneShown      paneID               // which one ovPane shows
	pendingNewForm *NewAgentForm        // New workspace form waiting for "+ Add project…" to finish
	detailsScroll  int                  // scroll offset of the details panel
	drag           dragTarget
	modelCache     map[string][]ModelInfo // provider -> models
	vpLines        []string               // rendered transcript lines, for mouse selection
	hist           *promptHistory
	// slash commands in the composer
	slash          *slashMenu
	slashDismissed string                    // the slash list closed with esc, until its "/" goes away
	commands       map[string][]slashCommand // agent -> its commands and skills
	cmdLoading     map[string]bool
	cmdErr         map[string]string
}

func NewModel(cfg BridgeConfig) Model {
	ta := textarea.New()
	ta.Placeholder = "Message the agent…"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetHeight(1)
	ta.MaxHeight = composerMaxLines
	ta.CharLimit = 0
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Prompt = sAccent
	ta.BlurredStyle.Prompt = sMuted
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	ta.Blur()

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = sYellow

	return Model{
		cfg:        cfg,
		hostKey:    cfg.HostLabel,
		state:      loadState(cfg.HostLabel),
		perms:      map[string][]PermissionReq{},
		progress:   map[string]*turnProgress{},
		lastTab:    map[string]string{},
		subs:       map[string]map[string]*ProviderSub{},
		subTabs:    map[string][]tabItem{},
		hist:       loadHistory(),
		modelCache: map[string][]ModelInfo{},
		panes:      map[paneID]*termPane{},
		commands:   map[string][]slashCommand{},
		cmdLoading: map[string]bool{},
		cmdErr:     map[string]string{},
		wsViews:    map[string]*wsView{},
		timeline:   NewTimeline(),
		render:     NewRenderer(lipgloss.HasDarkBackground()),
		vp:         viewport.New(80, 20),
		input:      ta,
		spin:       sp,
		follow:     true,
	}
}

func (m Model) Init() tea.Cmd {
	// Init has a value receiver, so the bridge is started from Update where state sticks.
	start := func() tea.Msg { return bridgeRestartMsg{} }
	return tea.Batch(start, m.spin.Tick, tea.SetWindowTitle("paseo"), checkForUpdateCmd())
}

// ---------- bridge plumbing ----------

func (m *Model) startBridge() tea.Cmd {
	br, err := StartBridge(m.cfg.Node, m.cfg.Script, m.cfg.Args, m.cfg.Env)
	if err != nil {
		m.down, m.downMsg = true, "cannot start bridge: "+err.Error()
		m.loaded = true
		return nil
	}
	m.br = br
	m.streamID = "" // the new bridge isn't watching anything yet
	return waitBridge(br)
}

func waitBridge(br *Bridge) tea.Cmd {
	return func() tea.Msg {
		first, ok := <-br.Events
		if !ok {
			return bridgeClosedMsg{br}
		}
		batch := []BridgeMsg{first}
		for len(batch) < 256 {
			select {
			case m, ok := <-br.Events:
				if !ok {
					return bridgeBatchMsg{br, batch}
				}
				batch = append(batch, m)
			default:
				return bridgeBatchMsg{br, batch}
			}
		}
		return bridgeBatchMsg{br, batch}
	}
}

// call runs a bridge request off the UI thread and reports it as an actionDoneMsg.
func (m *Model) call(what, op string, fields map[string]any) tea.Cmd {
	br := m.br
	if br == nil {
		return func() tea.Msg { return actionDoneMsg{what: what, err: fmt.Errorf("not connected")} }
	}
	return func() tea.Msg {
		_, err := br.Call(op, fields, 60*time.Second)
		return actionDoneMsg{what: what, err: err}
	}
}

func (m *Model) handleBridge(b BridgeMsg) tea.Cmd {
	switch b.Type {
	case "hello":
		m.local = strings.HasPrefix(b.Host, "home ")
	case "connected":
		m.connected, m.down, m.downMsg, m.connState = true, false, "", "connected"
	case "down":
		m.connected, m.down, m.downCode, m.downMsg, m.loaded = false, true, b.Code, b.Message, true
	case "conn":
		m.connState = b.State
	case "warn":
		m.setStatus(b.Message, true)
	case "fatal":
		m.down, m.downMsg, m.loaded = true, b.Message, true
	case "exit":
		m.connected = false
		m.setStatus(b.Message+" — restarting…", true)
	case "agents":
		m.loaded = true
		hadPerm := m.permHeight()
		m.agents = m.agents[:0]
		m.perms = map[string][]PermissionReq{}
		for _, s := range b.Agents {
			a := s.toAgent()
			m.agents = append(m.agents, a)
			if len(s.PendingPermissions) > 0 {
				m.perms[a.ID] = s.PendingPermissions
			}
			// keep progress in sync with the daemon's notion of the active turn
			if s.ActiveTurn != nil && s.ActiveTurn.StartedAt != nil {
				p := m.prog(a.ID)
				p.started = *s.ActiveTurn.StartedAt
			} else if p, ok := m.progress[a.ID]; ok && s.Status != "running" {
				p.started = time.Time{}
			}
		}
		if f := m.question; f != nil { // answered or withdrawn elsewhere: close the form
			still := false
			for _, p := range m.perms[f.agentID] {
				still = still || p.ID == f.requestID
			}
			if !still {
				m.question = nil
				if m.overlay == ovQuestion {
					m.overlay = ovNone
				}
			}
		}
		return m.reconcile(hadPerm)
	case "workspaces":
		hadPerm := m.permHeight()
		m.wsKnown = true
		m.workspaces = b.Workspaces
		m.emptyProjects = b.EmptyProjects
		return m.reconcile(hadPerm)
	case "history":
		if b.AgentID != m.streamID {
			return nil
		}
		m.timeline = NewTimeline()
		m.render.Reset()
		for _, it := range b.Items {
			m.timeline.Apply(it)
		}
		m.refreshViewport()
	case "event":
		if b.AgentID != m.streamID {
			return nil
		}
		m.applyEvent(b)
	case "subagent_update":
		m.applySubUpdate(b.Payload)
	case "project_update":
		// projects without workspaces only arrive here after the initial snapshot
		var p struct {
			Kind      string       `json:"kind"`
			Project   EmptyProject `json:"project"`
			ProjectID string       `json:"projectId"`
		}
		if json.Unmarshal(b.Payload, &p) == nil {
			keep := m.emptyProjects[:0]
			for _, e := range m.emptyProjects {
				if e.ProjectID != p.Project.ProjectID && e.ProjectID != p.ProjectID {
					keep = append(keep, e)
				}
			}
			m.emptyProjects = keep
			if p.Kind == "upsert" {
				m.emptyProjects = append(m.emptyProjects, p.Project)
			}
			return m.reconcile(m.permHeight())
		}
	}
	return nil
}

type streamEvent struct {
	Type   string          `json:"type"`
	Item   json.RawMessage `json:"item"`
	Usage  *Usage          `json:"usage"`
	Error  string          `json:"error"`
	Reason string          `json:"reason"`
}

func (m *Model) prog(id string) *turnProgress {
	p := m.progress[id]
	if p == nil {
		p = &turnProgress{}
		m.progress[id] = p
	}
	return p
}

func (m *Model) applyEvent(b BridgeMsg) {
	var ev streamEvent
	if json.Unmarshal(b.Event, &ev) != nil {
		return
	}
	p := m.prog(b.AgentID)
	switch ev.Type {
	case "timeline":
		m.timeline.Apply(ev.Item)
	case "turn_started":
		if t, err := time.Parse(time.RFC3339, b.Timestamp); err == nil {
			p.started = t
		} else {
			p.started = time.Now()
		}
		p.usage = nil
	case "usage_updated":
		p.usage = ev.Usage
	case "turn_completed":
		if ev.Usage != nil {
			p.usage = ev.Usage
		}
		p.started = time.Time{}
	case "turn_failed":
		p.started = time.Time{}
		m.timeline.push(&Entry{Kind: "error", Text: "Turn failed: " + ev.Error})
	case "turn_canceled":
		p.started = time.Time{}
		m.timeline.push(&Entry{Kind: "notice", Text: "interrupted", Level: "warning"})
	default:
		return
	}
	m.refreshViewport()
}

// markSeen clears the "finished, unseen" flag of the agent on screen, like the desktop app.
func (m *Model) markSeen() tea.Cmd {
	a := m.current()
	if a == nil || !a.Snap.RequiresAttention || a.Status == "running" || m.br == nil || !m.connected {
		return nil
	}
	a.Snap.RequiresAttention = false // optimistic; the daemon confirms via agent_update
	br, id := m.br, a.ID
	return func() tea.Msg {
		_, _ = br.Call("seen", map[string]any{"agentId": id}, 10*time.Second)
		return nil
	}
}

// watch points the transcript at the selected agent.
func (m *Model) watch() tea.Cmd {
	if a := m.agentByID(m.selectedID); a != nil {
		m.input.Placeholder = "Message " + truncate(agentLabel(*a), 48) + "…"
	}
	m.timeline = NewTimeline()
	m.render.Reset()
	m.follow = true
	m.detailsScroll = 0
	m.streamID = m.currentKey()
	m.refreshViewport()
	m.layout()
	if m.br == nil || !m.connected {
		return nil
	}
	br, id := m.br, m.selectedID
	if id == "" {
		return func() tea.Msg { _, _ = br.Call("unwatch", nil, 10*time.Second); return nil }
	}
	if ref := m.selSub; ref != nil {
		// provider subagents have no live agent stream of their own: fetch, then follow updates
		unwatch := func() tea.Msg { _, _ = br.Call("unwatch", nil, 10*time.Second); return nil }
		return tea.Batch(unwatch, m.fetchSubTimeline(*ref))
	}
	seen := m.markSeen()
	var models tea.Cmd // labels for the model badge in the tab bar
	if a := m.agentByID(id); a != nil {
		models = m.loadModels(a.Snap.Provider)
	}
	return tea.Batch(seen, models, m.fetchSubagents(id), func() tea.Msg {
		if _, err := br.Call("watch", map[string]any{"agentId": id, "tail": historyTail}, 30*time.Second); err != nil {
			return actionDoneMsg{what: "Load transcript", err: err}
		}
		return nil
	})
}

func (m *Model) current() *Agent { return m.agentByID(m.selectedID) }

func (m *Model) attach(paths ...string) {
	m.attachments = append(m.attachments, paths...)
	m.setStatus(fmt.Sprintf("Attached %d image(s)", len(paths)), false)
	m.layout()
}

func (m *Model) setStatus(s string, isErr bool) {
	m.status, m.isErr, m.statusT = s, isErr, time.Now()
}

func (m *Model) sidebarW() int {
	if m.state.HideSidebar {
		return 0
	}
	return m.sideWidth() + 1
}

// reconcile re-derives the sidebar after new data and keeps the selection valid.
func (m *Model) reconcile(hadPerm int) tea.Cmd {
	m.rebuildRows()
	var cmd tea.Cmd
	if m.wsViews[m.selWS] == nil {
		// the selected workspace is gone (or nothing selected yet): follow the cursor,
		// or the workspace of a freshly created agent
		if v := m.wsOfAgent(m.selectedID); v != nil {
			cmd = m.selectWorkspace(v.ws.ID, m.selectedID)
		} else if r := m.cursorRow(); r != nil && r.kind == rowWorkspace {
			cmd = m.selectWorkspace(r.key, "")
		} else if m.selWS != "" {
			m.selWS = ""
			cmd = m.selectTab("")
		}
	} else if m.selectedID == "" || !m.hasTab(m.currentKey()) {
		m.selSub = nil
		cmd = m.selectWorkspace(m.selWS, "")
	}
	if hadPerm != m.permHeight() {
		m.layout()
	}
	if cmd == nil && m.currentKey() != m.streamID {
		cmd = m.watch()
	}
	return tea.Batch(cmd, m.markSeen())
}

func hasAgent(v *wsView, id string) bool {
	for _, a := range v.agents {
		if a.ID == id {
			return true
		}
	}
	return false
}

// selectWorkspace opens a workspace and one of its tabs: the requested one,
// else the last one focused there, else the most recently active.
func (m *Model) selectWorkspace(wsID, agentID string) tea.Cmd {
	v := m.wsViews[wsID]
	if v == nil {
		return nil
	}
	m.selWS = wsID
	if agentID == "" {
		if last := m.lastTab[wsID]; last != "" {
			for _, t := range m.tabsOf(wsID) {
				if t.key() == last {
					return m.selectItem(t)
				}
			}
		}
	}
	pick := ""
	if agentID != "" && hasAgent(v, agentID) {
		pick = agentID
	} else if agentID != "" {
		for _, t := range m.tabsOf(wsID) {
			if t.key() == agentID {
				return m.selectItem(t)
			}
		}
	}
	if pick == "" {
		var best *Agent
		for i := range v.agents {
			if best == nil || v.agents[i].UpdatedAt.After(best.UpdatedAt) {
				best = &v.agents[i]
			}
		}
		if best != nil {
			pick = best.ID
		}
	}
	return m.selectTab(pick)
}

// currentKey identifies the focused tab (agent id, or "sub:parent/sub").
func (m *Model) currentKey() string {
	if m.selSub != nil {
		return tabItem{sub: m.selSub}.key()
	}
	return m.selectedID
}

func (m *Model) selectTab(agentID string) tea.Cmd {
	return m.selectItem(tabItem{agentID: agentID})
}

func (m *Model) selectItem(t tabItem) tea.Cmd {
	if t.key() != "" && m.selWS != "" {
		m.lastTab[m.selWS] = t.key()
	}
	if t.key() == m.currentKey() && t.key() == m.streamID {
		return nil
	}
	if t.sub != nil {
		m.selSub, m.selectedID = t.sub, t.sub.ParentID
	} else {
		m.selSub, m.selectedID = nil, t.agentID
	}
	if m.focus == focusInput && m.selSub != nil {
		m.focus = focusList
		m.input.Blur()
	}
	return m.watch()
}

func (m *Model) tabsOf(wsID string) []tabItem {
	saved := m.selWS
	m.selWS = wsID
	defer func() { m.selWS = saved }()
	return m.tabs()
}

// tabs are the selected workspace's agents, then any subagent tabs opened there.
func (m *Model) tabs() []tabItem {
	var out []tabItem
	if v := m.wsViews[m.selWS]; v != nil {
		for _, a := range v.agents {
			out = append(out, tabItem{agentID: a.ID})
		}
	}
	for _, t := range m.subTabs[m.selWS] {
		if t.sub != nil || m.agentByID(t.agentID) != nil {
			out = append(out, t)
		}
	}
	return out
}

func (m *Model) hasTab(key string) bool {
	for _, t := range m.tabs() {
		if t.key() == key {
			return true
		}
	}
	return false
}

func (m *Model) cycleTab(dir int) tea.Cmd {
	tabs := m.tabs()
	if len(tabs) < 2 {
		return nil
	}
	i := 0
	for j, t := range tabs {
		if t.key() == m.currentKey() {
			i = j
		}
	}
	return m.selectItem(tabs[(i+dir+len(tabs))%len(tabs)])
}

// openSubTab opens a subagent as a tab in the current workspace (or focuses it).
func (m *Model) openSubTab(t tabItem) tea.Cmd {
	if !m.hasTab(t.key()) {
		m.subTabs[m.selWS] = append(m.subTabs[m.selWS], t)
	}
	return m.selectItem(t)
}

// closeSubTab closes the focused subagent tab and returns to its parent.
func (m *Model) closeSubTab() tea.Cmd {
	cur := m.currentKey()
	list := m.subTabs[m.selWS]
	for i, t := range list {
		if t.key() == cur {
			m.subTabs[m.selWS] = append(list[:i:i], list[i+1:]...)
			parent := m.selectedID
			if t.sub == nil {
				if a := m.agentByID(t.agentID); a != nil {
					parent = childOf(*a)
				}
			}
			return m.selectWorkspace(m.selWS, parent)
		}
	}
	return nil
}

// treeOrder is every workspace in sidebar order, folded projects included,
// so { and } can walk the tree even when the sidebar isn't visible.
func (m *Model) treeOrder() []*wsView {
	var out []*wsView
	if m.state.GroupByStatus {
		for _, r := range m.rows {
			if r.kind == rowWorkspace {
				out = append(out, r.ws)
			}
		}
		// folded status groups: append their workspaces in group order too
		seen := map[string]bool{}
		for _, v := range out {
			seen[v.ws.ID] = true
		}
		for _, b := range statusOrder {
			for _, v := range m.wsViews {
				if v.bucket == b && !seen[v.ws.ID] {
					out = append(out, v)
				}
			}
		}
		return out
	}
	out = append(out, m.pinned...)
	for _, p := range m.projects {
		out = append(out, p.workspaces...)
	}
	return out
}

// stepTree opens the previous/next workspace in tree order, wrapping around.
func (m *Model) stepTree(dir int) tea.Cmd {
	order := m.treeOrder()
	if len(order) == 0 {
		return nil
	}
	i := -1
	for j, v := range order {
		if v.ws.ID == m.selWS {
			i = j
		}
	}
	next := 0
	if i >= 0 {
		next = (i + dir + len(order)) % len(order)
	} else if dir < 0 {
		next = len(order) - 1
	}
	v := order[next]
	m.revealWorkspace(v)
	m.sideScroll = 0
	m.setStatus(fmt.Sprintf("%s › %s  ·  %d/%d", v.ws.ProjectName(), v.title(), next+1, len(order)), false)
	return m.selectWorkspace(v.ws.ID, "")
}

// moveCursor moves through sidebar rows and opens workspaces it lands on.
func (m *Model) moveCursor(to int) tea.Cmd {
	if len(m.rows) == 0 {
		return nil
	}
	m.sideScroll = 0 // keyboard navigation brings the cursor back into view
	m.cursor = max(0, min(len(m.rows)-1, to))
	if r := m.cursorRow(); r != nil && r.kind == rowWorkspace && r.key != m.selWS {
		return m.selectWorkspace(r.key, "")
	}
	return nil
}

// step moves the cursor to the next selectable row (skipping section labels).
func (m *Model) step(dir int) tea.Cmd {
	for i := m.cursor + dir; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].selectable() {
			return m.moveCursor(i)
		}
	}
	return nil
}

// stepWorkspace jumps to the next workspace row (used when the sidebar is hidden).
func (m *Model) stepWorkspace(dir int) tea.Cmd {
	for i := m.cursor + dir; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].kind == rowWorkspace {
			return m.moveCursor(i)
		}
	}
	return nil
}

// selectAgent opens an agent from anywhere (palette, new agent): its workspace,
// expanded in the sidebar, with the agent's tab focused.
func (m *Model) selectAgent(id string) tea.Cmd {
	v := m.wsOfAgent(id)
	if v == nil {
		m.selectedID = id // not listed yet; reconcile picks it up when it arrives
		return nil
	}
	m.revealWorkspace(v)
	return m.selectWorkspace(v.ws.ID, id)
}

func (m *Model) revealWorkspace(v *wsView) {
	if k := m.projectKeyOf(v); m.state.Collapsed[k] {
		m.setCollapsed(k, false)
	}
	if i := m.rowOfWorkspace(v.ws.ID); i >= 0 {
		m.cursor = i
	}
}

func (m *Model) setCollapsed(project string, v bool) {
	if v {
		m.state.Collapsed[project] = true
	} else {
		delete(m.state.Collapsed, project)
	}
	saveState(m.hostKey, m.state)
	m.rebuildRows()
}

func (m *Model) layout() {
	rightW := m.mainW()
	m.input.SetWidth(rightW - 4) // inside the composer's border and padding
	vpH := m.height - 1 /*footer*/ - 2 /*workspace, tabs*/ - 1 /*status*/ - 2 /*box border*/ - m.input.Height() - m.permHeight() - m.attachHeight()
	m.vp.Width = rightW - 1
	m.vp.Height = max(3, vpH)
	m.render.SetWidth(rightW - 2)
	m.refreshViewport()
	if m.palette != nil {
		m.palette.SetSize(m.width, m.height)
	}
}

const composerMaxLines = 8

// fitInput grows or shrinks the message box to its content (wrapped lines count).
func (m *Model) fitInput() {
	w := max(1, m.input.Width())
	lines := 0
	for _, l := range strings.Split(m.input.Value(), "\n") {
		lines += max(1, (lipgloss.Width(l)+w-1)/w)
	}
	h := min(max(lines, 1), composerMaxLines)
	changed := h != m.composerH
	m.composerH = h
	m.input.SetHeight(h)
	if changed {
		m.layout()
	}
}

func (m *Model) permHeight() int {
	if a := m.current(); a != nil && len(m.perms[a.ID]) > 0 {
		return 4
	}
	return 0
}

func (m *Model) refreshViewport() {
	atBottom := m.follow || m.vp.AtBottom()
	content := m.render.Render(m.timeline)
	if strings.TrimSpace(content) == "" && m.current() != nil {
		content = sMuted.Render("\n  No activity yet.")
	}
	m.vp.SetContent(content)
	m.vpLines = strings.Split(content, "\n")
	if !m.sel.dragging {
		m.sel = selection{}
	}
	if atBottom {
		m.vp.GotoBottom()
	}
}

// ---------- update ----------

// Update handles a message, then keeps the terminal title on the selected workspace and tab.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	if t := nm.windowTitle(); t != nm.title {
		nm.title = t
		cmd = tea.Batch(cmd, tea.SetWindowTitle(t))
	}
	return nm, cmd
}

// windowTitle is "workspace · tab — project", or "paseo-tui" with nothing selected.
func (m *Model) windowTitle() string {
	v := m.wsViews[m.selWS]
	if v == nil {
		return "paseo-tui"
	}
	t := titleSafe(v.ws.DisplayName(), 40)
	if a := m.current(); a != nil {
		if l := titleSafe(agentLabel(*a), 30); l != "" && l != t {
			t += " · " + l
		}
	}
	return t + " — " + titleSafe(v.ws.ProjectName(), 30)
}

// titleSafe drops control characters, so a name can't end the title escape sequence and
// inject its own, and cuts it to max characters.
func titleSafe(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r < 0xa0) {
			continue
		}
		if n == max {
			b.WriteRune('…')
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for _, p := range m.runningPanes() {
			w, h := m.paneBox()
			p.resize(w-2, h-3)
		}
		m.layout()
		if m.form != nil {
			m.form.SetSize(m.width, m.height)
		}
		return m, nil

	case spinner.TickMsg:
		var c tea.Cmd
		m.spin, c = m.spin.Update(msg)
		return m, c

	case bridgeBatchMsg:
		if msg.br != m.br {
			return m, nil
		}
		var cmds []tea.Cmd
		for _, b := range msg.msgs {
			cmds = append(cmds, m.handleBridge(b))
		}
		cmds = append(cmds, waitBridge(m.br))
		return m, tea.Batch(cmds...)

	case bridgeClosedMsg:
		if msg.br != m.br {
			return m, nil
		}
		m.br, m.connected = nil, false
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return bridgeRestartMsg{} })

	case updateAvailableMsg:
		m.updateAvail, m.updateHint = msg.version, updateHint()
		return m, nil

	case bridgeRestartMsg:
		if m.br == nil {
			return m, m.startBridge()
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.setStatus(msg.what+" failed: "+msg.err.Error(), true)
		} else if msg.what != "" {
			m.setStatus(msg.what, false)
		}
		if msg.agentID != "" {
			return m, m.selectAgent(msg.agentID)
		}
		return m, nil

	case providersMsg:
		if msg.err == nil {
			m.providers = msg.provs
			if m.form != nil {
				m.form.SetProviders(msg.provs)
				// the provider may only be known now: fetch its models
				return m, m.loadModels(m.form.provider)
			}
		} else if m.form != nil {
			m.form.err = "providers: " + msg.err.Error()
		}
		return m, nil

	case historyMsg:
		if h := m.history; h != nil && msg.gen == h.gen {
			h.loading, h.err = false, ""
			if msg.err != nil {
				h.err = msg.err.Error()
			}
			h.entries = msg.entries
			h.sel = min(h.sel, max(0, len(h.entries)-1))
		}
		return m, nil

	case historySearchMsg:
		if h := m.history; h != nil && msg.gen == h.gen {
			h.loading = true
			return m, m.loadHistory(msg.gen, strings.TrimSpace(h.search.Value()))
		}
		return m, nil

	case reviveDoneMsg:
		if msg.err != nil {
			m.setStatus("Restore failed: "+msg.err.Error(), true)
			return m, nil
		}
		m.setStatus("Restored "+truncate(msg.name, 40), false)
		return m, m.selectAgent(msg.agentID)

	case commandsMsg:
		delete(m.cmdLoading, msg.agentID)
		if msg.err != nil {
			m.cmdErr[msg.agentID] = msg.err.Error()
		} else {
			m.commands[msg.agentID] = msg.cmds
			delete(m.cmdErr, msg.agentID)
		}
		var cmd tea.Cmd
		if m.slash != nil {
			cmd = m.refreshSlash()
		}
		return m, cmd

	case subagentListMsg:
		if msg.err == nil {
			list := map[string]*ProviderSub{}
			for i := range msg.subs {
				list[msg.subs[i].ID] = &msg.subs[i]
			}
			m.subs[msg.parentID] = list
		}
		m.layout()
		return m, nil

	case subTimelineMsg:
		if r := m.selSub; r == nil || *r != msg.ref {
			return m, nil
		}
		if msg.err != nil {
			m.setStatus("Subagent transcript: "+msg.err.Error(), true)
		}
		m.timeline = NewTimeline()
		m.render.Reset()
		for _, it := range msg.rows {
			m.timeline.Apply(it)
		}
		m.refreshViewport()
		return m, nil

	case newAgentDoneMsg:
		if f := m.form; f != nil {
			f.busy = false
			if msg.err != nil {
				f.err = msg.err.Error()
				return m, nil
			}
		}
		if msg.err != nil {
			m.setStatus("Start agent failed: "+msg.err.Error(), true)
			return m, nil
		}
		m.overlay, m.form = ovNone, nil
		m.setStatus("Agent started", false)
		if msg.agentID != "" {
			return m, m.selectAgent(msg.agentID)
		}
		return m, nil

	case paneOutputMsg:
		if msg.p != m.panes[msg.p.id()] {
			return m, nil
		}
		return m, waitPane(msg.p)

	case paneExitMsg:
		if msg.p != m.panes[msg.p.id()] {
			return m, nil
		}
		delete(m.panes, msg.p.id())
		if m.overlay == ovPane && m.paneShown == msg.p.id() {
			m.overlay = ovNone
		}
		if msg.err != nil {
			m.setStatus(msg.p.name+" exited: "+msg.err.Error(), true)
		}
		return m, nil

	case forkContextMsg:
		if f := m.form; f != nil && f.fork == msg.src {
			if msg.err != nil {
				f.fork.err = msg.err.Error()
			} else {
				f.fork.context, f.fork.items = msg.context, msg.items
				if f.err == "Still loading the chat history…" {
					f.err = ""
				}
			}
		}
		return m, nil

	case branchesMsg:
		if m.form != nil && msg.err == nil {
			m.form.branches[msg.root] = msg.branches
		}
		return m, nil

	case modelsMsg:
		if msg.err == nil {
			m.modelCache[msg.provider] = msg.models
			if m.form != nil {
				m.form.models[msg.provider] = msg.models
			}
		}
		if st := m.settings; st != nil && st.provider == msg.provider {
			st.loading, st.models = false, msg.models
			if msg.err != nil {
				st.err = "models: " + msg.err.Error()
			}
		}
		return m, nil

	case projectAddedMsg:
		if f := m.projForm; f != nil {
			f.busy = false
			if msg.err != nil {
				f.err = msg.err.Error()
				m.setStatus("Add project failed", true)
				return m, nil
			}
		}
		if msg.err != nil {
			m.setStatus("Add project failed: "+msg.err.Error(), true)
			return m, nil
		}
		m.overlay, m.projForm = ovNone, nil
		name := orDefault(msg.project.ProjectDisplayName, path.Base(msg.project.ProjectRootPath))
		m.setStatus("Added project "+name, false)
		// like the desktop: continue straight into a new workspace in that project
		p := projOpt{id: msg.project.ProjectID, name: name, root: msg.project.ProjectRootPath,
			git: msg.project.ProjectKind == "git" || msg.project.ProjectKind == ""}
		if f := m.pendingNewForm; f != nil {
			m.pendingNewForm = nil
			f.projects = append(f.projects, p)
			f.projIdx, f.picking = len(f.projects)-1, false
			f.fixIso()
			m.form, m.overlay = f, ovNew
			return m, m.loadBranches(p.root)
		}
		mm, cmd := m.openNewFormMode(false, "")
		m = mm.(Model)
		m.form.projects = append(m.form.projects, p)
		m.form.projIdx = len(m.form.projects) - 1
		m.form.fixIso()
		return m, tea.Batch(cmd, m.loadBranches(p.root))

	case clipboardImageMsg:
		if m.form != nil && !msg.none {
			m.form.pasting = false
		}
		switch {
		case msg.err != nil:
			m.setStatus("Paste image: "+msg.err.Error(), true)
		case msg.none:
			// no image on the clipboard: behave like a normal text paste
			var c tea.Cmd
			if m.overlay == ovNew && m.form != nil {
				_, _, _, c = m.form.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
			} else if m.focus == focusInput {
				m.input.SetHeight(composerMaxLines)
				m.input, c = m.input.Update(tea.KeyMsg{Type: tea.KeyCtrlV})
				m.fitInput()
			}
			return m, c
		case m.overlay == ovNew && m.form != nil:
			m.form.attachments = append(m.form.attachments, msg.path)
		default:
			m.attach(msg.path)
		}
		return m, nil

	case tea.MouseMsg:
		if m.overlay == ovPane {
			m.paneMouse(msg)
			return m, nil
		}
		// a click dismisses floating panels (help, command center, subagent list)
		if (m.overlay == ovHelp || m.overlay == ovPalette || m.overlay == ovSubs || m.overlay == ovSettings || m.overlay == ovMenu) &&
			msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			m.overlay, m.palette, m.subList, m.settings, m.menu = ovNone, nil, nil, nil, nil
			return m, nil
		}
		if m.overlay == ovNone {
			if m.dragDivider(msg) {
				return m, nil
			}
			if handled, c := m.clickSidebar(msg); handled {
				return m, c
			}
			if m.detailsW() > 0 && msg.X >= m.sidebarW()+m.mainW() {
				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.scrollDetails(-3)
				case tea.MouseButtonWheelDown:
					m.scrollDetails(3)
				}
				return m, nil // clicks in the panel don't fall through to the transcript
			}
			if handled, c := m.clickTab(msg); handled {
				return m, c
			}
			if handled, c := m.clickMain(msg); handled {
				return m, c
			}
			if handled, c := m.handleMouse(msg); handled {
				return m, c
			}
			if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
				m.wheel(msg.Button == tea.MouseButtonWheelUp)
			}
			return m, nil
		}
		return m, nil

	case openNewMsg:
		return m.openNewForm()

	case copiedMsg:
		if msg.err != nil {
			m.setStatus("Copy failed: "+msg.err.Error(), true)
		} else {
			m.setStatus(fmt.Sprintf("Copied %d characters", msg.n), false)
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.overlay == ovPane { // the pane's program gets every key
		m.paneKey(k)
		return m, nil
	}
	if k.String() == "ctrl+c" {
		if m.overlay == ovConfirm && m.askingQuit { // a second ctrl+c confirms
			m.shutdown()
			return m, tea.Quit
		}
		return m.quit()
	}

	switch m.overlay {
	case ovHelp:
		m.overlay = ovNone
		return m, nil
	case ovConfirm:
		key := k.String()
		if m.askingQuit && key == "q" { // q, q quits
			key = "y"
		}
		switch key {
		case "y", "Y", "enter":
			m.overlay, m.askingQuit = ovNone, false
			fn := m.confirmFn
			m.confirmFn = nil
			if fn != nil {
				return m, fn()
			}
		case "n", "N", "esc", "q":
			m.overlay, m.askingQuit = ovNone, false
		}
		return m, nil
	case ovSettings:
		return m, m.settingsKey(k)
	case ovMenu:
		return m, m.menuKey(k)
	case ovProject:
		return m, m.projectFormKey(k)
	case ovRename:
		return m, m.renameKey(k)
	case ovQuestion:
		return m, m.questionKey(k)
	case ovHistory:
		return m, m.historyKey(k)
	case ovSubs:
		done, pick := m.subList.Update(k)
		if done {
			m.overlay, m.subList = ovNone, nil
			if pick != nil {
				return m, m.openSubTab(*pick)
			}
		}
		return m, nil
	case ovPalette:
		done, pick, c := m.palette.Update(k)
		if done {
			m.overlay = ovNone
			m.palette = nil
			if pick != nil {
				if pick.action != "" {
					// actions are the same as their keyboard shortcut
					return m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pick.action)})
				}
				if v := m.wsViews[pick.wsID]; v != nil {
					m.revealWorkspace(v)
					return m, m.selectWorkspace(pick.wsID, pick.agentID)
				}
			}
		}
		return m, c
	case ovNew:
		f := m.form
		prevRoot, prevProv := "", f.provider
		if p := f.project(); p != nil {
			prevRoot = p.root
		}
		done, submit, action, c := f.Update(k)
		if action == "add-project" {
			m.overlay = ovNone
			mm, cmd := m.openProjectForm()
			mm2 := mm.(Model)
			mm2.pendingNewForm = f // come back to this form once the project exists
			return mm2, cmd
		}
		m.rememberNewChoices(f)
		var loads []tea.Cmd
		if p := f.project(); p != nil && p.root != prevRoot {
			loads = append(loads, m.loadBranches(p.root))
		}
		if f.provider != prevProv {
			loads = append(loads, m.loadModels(f.provider))
		}
		if done && !submit {
			m.overlay, m.form = ovNone, nil
			return m, nil
		}
		if submit {
			return m, m.submitNewForm()
		}
		return m, tea.Batch(append(loads, c)...)
	}

	// ctrl+n / ctrl+w open a new agent tab / close a subagent tab, from anywhere;
	// alt+↑/↓ (alt+k/j) scroll the details panel
	switch k.String() {
	case "alt+up", "alt+k":
		if m.detailsW() > 0 {
			m.scrollDetails(-3)
			return m, nil
		}
	case "alt+down", "alt+j":
		if m.detailsW() > 0 {
			m.scrollDetails(3)
			return m, nil
		}
	case "ctrl+n":
		return m.openNewForm()
	case "ctrl+w":
		return m, m.closeSubTab()
	}
	if k.String() == "esc" && m.sel.active && m.focus != focusInput {
		m.sel = selection{}
		return m, nil
	}

	// alt+1..9 jump straight to a tab, from anywhere
	if ks := k.String(); len(ks) == 5 && strings.HasPrefix(ks, "alt+") && ks[4] >= '1' && ks[4] <= '9' {
		if tabs := m.tabs(); int(ks[4]-'1') < len(tabs) {
			return m, m.selectItem(tabs[ks[4]-'1'])
		}
		return m, nil
	}

	if m.focus == focusInput {
		if m.slash != nil && m.slashKey(k) {
			return m, nil
		}
		if k.Paste {
			if paths := imagePathsIn(string(k.Runes)); len(paths) > 0 {
				m.attach(paths...)
				return m, nil
			}
		}
		switch k.String() {
		case "ctrl+v":
			return m, readClipboardImage
		case "up":
			if m.input.Line() == 0 {
				var prompts []string
				for _, e := range m.timeline.Entries {
					if e.Kind == "user" {
						prompts = append(prompts, e.Text)
					}
				}
				if text, ok := m.hist.older(m.input.Value(), prompts); ok {
					m.input.SetHeight(composerMaxLines)
					m.input.SetValue(text)
					m.fitInput()
					m.input.CursorStart()
					for m.input.Line() > 0 {
						m.input.CursorUp()
					}
				}
				return m, nil
			}
		case "down":
			if m.input.Line() >= m.input.LineCount()-1 {
				if text, ok := m.hist.newer(); ok {
					m.input.SetHeight(composerMaxLines)
					m.input.SetValue(text)
					m.fitInput()
				}
				return m, nil
			}
		case "backspace":
			if m.input.Value() == "" && len(m.attachments) > 0 {
				m.attachments = m.attachments[:len(m.attachments)-1]
				m.layout()
				return m, nil
			}
		case "tab":
			return m, m.cycleTab(1)
		case "shift+tab":
			return m, m.cycleTab(-1)
		case "esc":
			m.focus, m.slash = focusList, nil
			m.input.Blur()
			m.hist.reset()
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			a := m.current()
			if (text == "" && len(m.attachments) == 0) || a == nil {
				return m, nil
			}
			fields := map[string]any{"agentId": a.ID, "text": text}
			if len(m.attachments) > 0 {
				fields["images"] = m.attachments
				m.attachments = nil
				m.layout()
			}
			m.hist.add(text)
			m.input.Reset()
			m.fitInput()
			m.follow = true
			m.setStatus("Sending…", false)
			return m, m.call("Sent", "send", fields)
		case "pgup", "pgdown", "ctrl+u", "ctrl+d":
			return m.scroll(k.String())
		case "ctrl+p":
			return m.openPalette()
		}
		var c tea.Cmd
		m.input.SetHeight(composerMaxLines) // edit at full height so the box never scrolls when it fits
		m.input, c = m.input.Update(k)
		m.fitInput()
		cmd := m.refreshSlash()
		return m, tea.Batch(c, cmd)
	}

	a := m.current()
	switch k.String() {
	case "q":
		return m.quit()
	case "?":
		m.overlay = ovHelp
	case "p", "ctrl+p":
		return m.openPalette()
	case "/":
		// write a slash command to the agent; without one, the command center
		if a == nil || m.selSub != nil {
			return m.openPalette()
		}
		v := m.input.Value()
		if v != "" && !strings.HasSuffix(v, " ") && !strings.HasSuffix(v, "\n") {
			v += " "
		}
		m.input.SetHeight(composerMaxLines)
		m.input.SetValue(v + "/")
		m.fitInput()
		m.focus = focusInput
		cmd := tea.Batch(m.input.Focus(), m.refreshSlash())
		return m, cmd
	case "J", "down":
		if m.state.HideSidebar {
			return m, m.stepWorkspace(1)
		}
		return m, m.step(1)
	case "K", "up":
		if m.state.HideSidebar {
			return m, m.stepWorkspace(-1)
		}
		return m, m.step(-1)
	case "home":
		return m, m.moveCursor(0)
	case "end":
		return m, m.moveCursor(len(m.rows) - 1)
	case "tab":
		return m, m.cycleTab(1)
	case "shift+tab":
		return m, m.cycleTab(-1)
	case "]":
		return m, m.cycleTab(1)
	case "[":
		return m, m.cycleTab(-1)
	case "}":
		return m, m.stepTree(1)
	case "{":
		return m, m.stepTree(-1)
	case "h", "left":
		if r := m.cursorRow(); r != nil {
			key := r.key
			if r.kind == rowWorkspace {
				key = m.projectKeyOf(r.ws)
			}
			if r.kind == rowWorkspace || r.kind == rowProject {
				m.setCollapsed(key, true)
				for i, rr := range m.rows {
					if rr.kind == rowProject && rr.key == key {
						m.cursor = i
					}
				}
			}
		}
	case "l", "right":
		if r := m.cursorRow(); r != nil && r.kind == rowProject {
			if m.state.Collapsed[r.key] {
				m.setCollapsed(r.key, false)
			} else {
				return m, m.step(1)
			}
		}
	case " ":
		if r := m.cursorRow(); r != nil && r.kind == rowProject {
			m.setCollapsed(r.key, !m.state.Collapsed[r.key])
		}
	case "z":
		// collapse every group except the one holding the open workspace
		keep := ""
		if v := m.wsViews[m.selWS]; v != nil {
			keep = m.projectKeyOf(v)
		}
		anyOpen := false
		for _, r := range m.rows {
			if r.kind == rowProject && r.key != keep && !m.state.Collapsed[r.key] {
				anyOpen = true
			}
		}
		for _, r := range m.rows {
			if r.kind == rowProject && r.key != keep {
				if anyOpen {
					m.state.Collapsed[r.key] = true
				}
			}
		}
		if !anyOpen {
			for k := range m.state.Collapsed {
				delete(m.state.Collapsed, k)
			}
		}
		m.setCollapsed(keep, false)
		if v := m.wsViews[m.selWS]; v != nil {
			m.revealWorkspace(v)
		}
	case "o":
		m.state.GroupByStatus = !m.state.GroupByStatus
		saveState(m.hostKey, m.state)
		m.rebuildRows()
		if v := m.wsViews[m.selWS]; v != nil {
			m.revealWorkspace(v)
		}
	case "<", ">", ",", ".":
		if !m.resizeKey(k.String()) {
			m.setStatus("Nothing to resize here", true)
		}
	case "d":
		m.state.ShowDetails = !m.state.ShowDetails
		saveState(m.hostKey, m.state)
		m.layout()
		if m.state.ShowDetails && m.detailsW() == 0 {
			m.setStatus("Window too narrow for the details panel", true)
		}
	case "b", "ctrl+b":
		m.state.HideSidebar = !m.state.HideSidebar
		saveState(m.hostKey, m.state)
		m.layout()
	case "enter", "i":
		if r := m.cursorRow(); k.String() == "enter" && r != nil && r.kind == rowProject && !m.state.HideSidebar {
			m.setCollapsed(r.key, !m.state.Collapsed[r.key])
			return m, nil
		}
		if m.selSub != nil {
			m.setStatus("Subagent transcripts are read-only — message the parent agent's tab", true)
			return m, nil
		}
		if a != nil {
			if p, qs, ok := m.pendingQuestion(a.ID); ok {
				return m.openQuestion(a.ID, p, qs)
			}
			m.focus = focusInput
			return m, m.input.Focus()
		}
	case "pgup", "pgdown", "ctrl+u", "ctrl+d", "g", "G", "j", "k":
		return m.scroll(k.String())
	case "n", "c":
		return m.openNewForm()
	case "N":
		return m.openNewFormMode(false, "")
	case "m":
		return m.openSettings()
	case "f":
		return m.openFork()
	case "R":
		return m.openRename()
	case "H":
		return m.openHistory()
	case "e":
		return m.openPane(paneEditor)
	case "F":
		return m.openPane(paneFiles)
	case "u":
		if m.selectedID != "" {
			parent := m.selectedID
			name := parent
			if a := m.current(); a != nil {
				name = agentLabel(*a)
			}
			m.subList = &SubList{parent: truncate(name, 50), entries: m.subagentsOf(parent), width: m.width, height: m.height}
			m.overlay = ovSubs
			return m, m.fetchSubagents(parent)
		}
	case "w", "ctrl+w":
		return m, m.closeSubTab()
	case "s":
		if a != nil {
			m.setStatus("Interrupting…", false)
			return m, m.call("Interrupted", "cancel", map[string]any{"agentId": a.ID})
		}
	case "a":
		return m.openArchiveMenu()
	case "D", "delete":
		if a != nil {
			id, name := a.ID, agentLabel(*a)
			return m.confirm(fmt.Sprintf("Permanently delete %q?", name), func() tea.Cmd {
				return m.call("Deleted "+name, "delete", map[string]any{"agentId": id})
			})
		}
	case "y", "x":
		if a != nil && len(m.perms[a.ID]) > 0 {
			if p, qs, ok := m.pendingQuestion(a.ID); ok {
				if k.String() == "y" {
					return m.openQuestion(a.ID, p, qs)
				}
				return m, m.dismissQuestion(a.ID, p, qs)
			}
			p := m.perms[a.ID][0]
			allow := k.String() == "y"
			verb := map[bool]string{true: "Allowed", false: "Denied"}[allow]
			return m, m.call(verb+" "+p.Name, "permit", map[string]any{"agentId": a.ID, "requestId": p.ID, "allow": allow})
		}
	case "A":
		m.showAll = !m.showAll
		m.setStatus(map[bool]string{true: "Showing archived agents", false: "Hiding archived agents"}[m.showAll], false)
		return m, m.call("", "archived", map[string]any{"value": m.showAll})
	case "t":
		m.render.ShowThk = !m.render.ShowThk
		m.refreshViewport()
	case "v":
		m.render.Verbose = !m.render.Verbose
		m.refreshViewport()
	case "r", "ctrl+r":
		if m.br != nil && !m.connected {
			return m, m.call("Reconnected", "reconnect", nil)
		}
		return m, m.watch()
	case "S":
		if m.down && m.local {
			m.setStatus("Starting daemon…", false)
			return m, m.call("Daemon started", "startDaemon", nil)
		}
	}
	return m, nil
}

// Scroll steps in lines. A trackpad sends a wheel event every few
// milliseconds, so the ramp is gentle: a single notch moves wheelBase lines, a
// long fast spin works up to wheelMax per event.
const (
	wheelBase = 2
	wheelMax  = 8
	lineStep  = 3 // j / k
)

// wheel scrolls the transcript, speeding up while the wheel keeps spinning.
func (m *Model) wheel(up bool) {
	if time.Since(m.lastWheel) < 60*time.Millisecond {
		m.wheelSpeed = min(m.wheelSpeed+1, wheelMax)
	} else {
		m.wheelSpeed = wheelBase
	}
	m.lastWheel = time.Now()
	if up {
		m.vp.LineUp(m.wheelSpeed)
	} else {
		m.vp.LineDown(m.wheelSpeed)
	}
	m.follow = m.vp.AtBottom()
}

func (m Model) scroll(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "pgup":
		m.vp.ViewUp()
	case "pgdown":
		m.vp.ViewDown()
	case "ctrl+u":
		m.vp.HalfViewUp()
	case "ctrl+d":
		m.vp.HalfViewDown()
	case "k":
		m.vp.LineUp(lineStep)
	case "j":
		m.vp.LineDown(lineStep)
	case "g":
		m.vp.GotoTop()
	case "G":
		m.vp.GotoBottom()
	}
	m.follow = m.vp.AtBottom()
	return m, nil
}

func (m Model) confirm(text string, fn func() tea.Cmd) (tea.Model, tea.Cmd) {
	m.overlay, m.confirmText, m.confirmFn = ovConfirm, text, fn
	return m, nil
}

func (m Model) openPalette() (tea.Model, tea.Cmd) {
	m.palette = NewPalette(&m)
	m.palette.SetSize(m.width, m.height)
	m.overlay = ovPalette
	if m.focus == focusInput {
		m.focus = focusList
		m.input.Blur()
	}
	return m, m.palette.Init()
}

func (m Model) openNewForm() (tea.Model, tea.Cmd) { return m.openNewFormMode(true, "") }

// openNewFormMode opens the New workspace form. asTab starts on "This workspace"
// (a new tab, like the desktop's New tab); projectID preselects a project.
func (m Model) openNewFormMode(asTab bool, projectID string) (tea.Model, tea.Cmd) {
	f := &NewAgentForm{
		prompt:   newPrompt(),
		branches: map[string][]string{},
		models:   map[string][]ModelInfo{},
		focus:    -1,
	}
	for prov, list := range m.modelCache {
		f.models[prov] = list
	}
	for _, p := range m.projects {
		if p.root != "" && !strings.HasPrefix(p.id, "cwd:") {
			f.projects = append(f.projects, projOpt{id: p.id, name: p.name, root: p.root, git: p.git})
		}
	}
	// the open workspace and its project
	if v := m.wsViews[m.selWS]; v != nil && !v.ws.synthetic {
		f.wsID, f.wsName, f.wsDir, f.wsProject = v.ws.ID, v.ws.ProjectName()+" › "+v.ws.DisplayName(), v.ws.WorkspaceDirectory, v.ws.ProjectID
		if projectID == "" {
			projectID = v.ws.ProjectID
		}
	}
	for i, p := range f.projects {
		if p.id == projectID {
			f.projIdx = i
		}
	}
	// remembered choices, then the focused agent's provider
	// new workspaces default to a worktree, or whatever was chosen last
	f.preferIso = isoWorktree
	if m.state.NewIsolation == "local" {
		f.preferIso = isoLocal
	}
	f.iso = f.preferIso
	if asTab && f.canThis() {
		f.iso = isoThis
	}
	f.fixIso()
	f.provider, f.model, f.thinking = m.state.NewProvider, m.state.NewModel, m.state.NewThinking
	if a := m.current(); a != nil && f.provider == "" {
		f.provider, f.model = a.Snap.Provider, currentModel(a)
	}
	if m.providers != nil {
		f.SetProviders(m.providers)
	}
	f.SetSize(m.width, m.height)
	m.form = f
	m.overlay = ovNew

	cmds := []tea.Cmd{f.Init(), m.loadModels(f.provider)}
	if p := f.project(); p != nil {
		cmds = append(cmds, m.loadBranches(p.root))
	}
	if m.providers == nil && m.br != nil {
		br := m.br
		cmds = append(cmds, func() tea.Msg {
			data, err := br.Call("providers", nil, 30*time.Second)
			var p []Provider
			if err == nil {
				err = json.Unmarshal(data, &p)
			}
			return providersMsg{provs: p, err: err}
		})
	}
	return m, tea.Batch(cmds...)
}

// openFork opens the New agent form as a fork of the focused agent: a new tab in
// this workspace (or, via Where, a new workspace) that starts from its chat history.
func (m Model) openFork() (tea.Model, tea.Cmd) {
	a := m.current()
	if a == nil || m.selSub != nil {
		m.setStatus("Open an agent tab to fork it", true)
		return m, nil
	}
	if m.br == nil || !m.connected {
		m.setStatus("Not connected to the daemon", true)
		return m, nil
	}
	mm, cmd := m.openNewFormMode(true, "")
	m = mm.(Model)
	f := m.form
	src := &forkSource{
		agentID: a.ID, name: agentLabel(*a), provider: a.Snap.Provider, cwd: a.Cwd,
		modeID: deref(a.Snap.CurrentModeID), options: a.Snap.Options,
	}
	f.fork = src
	f.provider, f.model, f.thinking = a.Snap.Provider, currentModel(a), deref(a.Snap.ThinkingOptionID)
	f.prompt.Placeholder = "What should the fork do next?"
	br := m.br
	load := func() tea.Msg {
		data, err := br.Call("forkContext", map[string]any{"agentId": src.agentID}, 30*time.Second)
		var res struct {
			Attachment json.RawMessage `json:"attachment"`
			ItemCount  int             `json:"itemCount"`
		}
		if err == nil {
			err = json.Unmarshal(data, &res)
		}
		return forkContextMsg{src: src, context: res.Attachment, items: res.ItemCount, err: err}
	}
	return m, tea.Batch(cmd, m.loadModels(f.provider), load)
}

func (m *Model) loadModels(provider string) tea.Cmd {
	if provider == "" || m.br == nil {
		return nil
	}
	if _, ok := m.modelCache[provider]; ok {
		return nil
	}
	br := m.br
	return func() tea.Msg {
		data, err := br.Call("models", map[string]any{"provider": provider}, 30*time.Second)
		var res struct {
			Models []ModelInfo `json:"models"`
		}
		if err == nil {
			err = json.Unmarshal(data, &res)
		}
		return modelsMsg{provider: provider, models: res.Models, err: err}
	}
}

func (m *Model) loadBranches(root string) tea.Cmd {
	if root == "" || m.br == nil {
		return nil
	}
	if m.form != nil {
		if _, ok := m.form.branches[root]; ok {
			return nil
		}
	}
	br := m.br
	return func() tea.Msg {
		data, err := br.Call("branches", map[string]any{"cwd": root}, 20*time.Second)
		var b []string
		if err == nil {
			err = json.Unmarshal(data, &b)
		}
		return branchesMsg{root: root, branches: b, err: err}
	}
}

// rememberNewChoices saves the form's choices whenever they change, like the desktop.
// A fork's choices come from its source agent, so they aren't remembered.
func (m *Model) rememberNewChoices(f *NewAgentForm) {
	if f.fork != nil {
		return
	}
	iso := map[int]string{isoLocal: "local", isoWorktree: "worktree"}[f.preferIso]
	if iso == m.state.NewIsolation && f.provider == m.state.NewProvider &&
		f.model == m.state.NewModel && f.thinking == m.state.NewThinking {
		return
	}
	if iso != "" {
		m.state.NewIsolation = iso
	}
	if f.provider != "" {
		m.state.NewProvider, m.state.NewModel, m.state.NewThinking = f.provider, f.model, f.thinking
	}
	saveState(m.hostKey, m.state)
}

type newAgentDoneMsg struct {
	agentID, workspaceID string
	err                  error
}

func (m *Model) submitNewForm() tea.Cmd {
	f := m.form
	op, fields := f.Request()
	m.rememberNewChoices(f)
	f.busy, f.err = true, ""
	br := m.br
	if br == nil {
		f.busy, f.err = false, "not connected"
		return nil
	}
	return func() tea.Msg {
		data, err := br.Call(op, fields, 10*time.Minute)
		var res struct {
			WorkspaceID string `json:"workspaceId"`
			AgentID     string `json:"agentId"`
		}
		if err == nil {
			_ = json.Unmarshal(data, &res)
		}
		return newAgentDoneMsg{agentID: res.AgentID, workspaceID: res.WorkspaceID, err: err}
	}
}

// quit asks before exiting; the question warns when hidden panes would close with it.
func (m Model) quit() (tea.Model, tea.Cmd) {
	q := "Quit paseo-tui?"
	if ps := m.runningPanes(); len(ps) > 0 {
		var names []string
		for _, p := range ps {
			names = append(names, p.name+" ("+filepath.Base(p.dir)+")")
		}
		q = fmt.Sprintf("Quit paseo-tui? %s still running in the background and will close too.\n\nUnsaved changes there are lost.",
			strings.Join(names, ", ")+map[bool]string{true: " is", false: " are"}[len(names) == 1])
	}
	m.askingQuit = true
	return m.confirm(q, func() tea.Cmd {
		m.shutdown()
		return tea.Quit
	})
}

func (m *Model) shutdown() {
	if m.br != nil {
		m.br.Close()
	}
	for _, p := range m.runningPanes() {
		p.kill()
	}
}

// ---------- view helpers ----------

func agentLabel(a Agent) string {
	if a.Name != "" && a.Name != "-" {
		return a.Name
	}
	return a.ShortID
}

func (m Model) statusDot(a Agent) string {
	switch {
	case len(m.perms[a.ID]) > 0:
		return sRed.Bold(true).Render("!")
	case a.Status == "running":
		return m.spin.View()
	case a.Snap.RequiresAttention:
		return sBlue.Render("◆") // finished, not yet looked at
	case a.Status == "idle":
		return sGreen.Render("●")
	case a.Status == "initializing":
		return sYellow.Render("◌")
	case a.Status == "error":
		return sRed.Render("●")
	}
	return sMuted.Render("○")
}

func elapsed(t time.Time) string {
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	if d < time.Hour {
		return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// turnStarted is when the agent's current turn began, if it is running.
func (m Model) turnStarted(a Agent) time.Time {
	if a.Status != "running" {
		return time.Time{}
	}
	if p := m.progress[a.ID]; p != nil && !p.started.IsZero() {
		return p.started
	}
	if t := a.Snap.ActiveTurn; t != nil && t.StartedAt != nil {
		return *t.StartedAt
	}
	return time.Time{}
}

// activityLine describes what the watched agent is doing right now.
func (m Model) activityLine(a Agent) string {
	what := "Working"
	for i := len(m.timeline.Entries) - 1; i >= 0; i-- {
		e := m.timeline.Entries[i]
		if e.Kind == "tool" && e.Tool.Status == "running" {
			what = "Running " + toolSummary(e.Tool)
			break
		}
		if e.Kind == "reasoning" {
			what = "Thinking"
			break
		}
		if e.Kind == "assistant" {
			what = "Writing"
			break
		}
		if e.Kind == "tool" || e.Kind == "user" {
			break
		}
	}
	s := m.spin.View() + " " + sYellow.Render(what)
	if t := m.turnStarted(a); !t.IsZero() {
		s += sMuted.Render(" · " + elapsed(t))
	}
	if p := m.progress[a.ID]; p != nil && p.usage != nil {
		s += sMuted.Render(fmt.Sprintf(" · ↑%s ↓%s", humanTokens(p.usage.InputTokens+p.usage.CachedInputTokens), humanTokens(p.usage.OutputTokens)))
	}
	return s
}

// permissionSummary says what a permission request is for: the command or
// file from its tool detail, falling back to its description.
func permissionSummary(p PermissionReq) string {
	var d ToolDetail
	if len(p.Detail) > 0 && json.Unmarshal(p.Detail, &d) == nil {
		switch {
		case d.Command != "":
			return homeRelative(firstLine(d.Command))
		case d.FilePath != "":
			return homeRelative(d.FilePath)
		case d.URL != "":
			return d.URL
		}
	}
	return orDefault(p.Description, p.Title)
}

func toolSummary(t *ToolEntry) string {
	d := t.Detail
	switch d.Type {
	case "shell":
		return "Bash: " + homeRelative(firstLine(d.Command))
	case "read", "write", "edit":
		return strings.ToUpper(d.Type[:1]) + d.Type[1:] + " " + homeRelative(d.FilePath)
	case "search":
		return "Search: " + d.Query
	case "fetch":
		return "Fetch " + d.URL
	case "sub_agent":
		return "subagent: " + d.Description
	}
	return t.Name
}

// ---------- view ----------

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	footer := m.viewFooter()
	bodyH := m.height - 1

	main := m.viewMain(bodyH)
	if m.detailsW() > 0 {
		sep := lipgloss.NewStyle().Foreground(m.dividerColor(dragDetails)).Render(strings.Repeat("│\n", bodyH-1) + "│")
		main = lipgloss.JoinHorizontal(lipgloss.Top, main, sep, m.viewDetails(bodyH))
	}
	body := main
	if !m.state.HideSidebar {
		side := m.viewSidebar(bodyH)
		sep := lipgloss.NewStyle().Foreground(m.dividerColor(dragSidebar)).Render(strings.Repeat("│\n", bodyH-1) + "│")
		body = lipgloss.JoinHorizontal(lipgloss.Top, side, sep, main)
	}
	screen := lipgloss.JoinVertical(lipgloss.Left, body, footer)

	switch m.overlay {
	case ovHelp:
		return m.centerOver(screen, helpBox())
	case ovConfirm:
		text := lipgloss.NewStyle().Width(min(64, m.width-16)).Render(m.confirmText)
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cRed).Padding(1, 3).
			Render(text + "\n\n" + sMuted.Render("y confirm · n cancel"))
		return m.centerOver(screen, box)
	case ovNew:
		return m.centerOver(screen, m.form.View(m))
	case ovPalette:
		return m.palette.Overlay(m, screen)
	case ovSubs:
		return m.centerOver(screen, m.subList.View(m))
	case ovSettings:
		return m.centerOver(screen, m.settingsView())
	case ovMenu:
		return m.centerOver(screen, m.menuView())
	case ovProject:
		return m.centerOver(screen, m.projectFormView())
	case ovRename:
		return m.centerOver(screen, m.renameView())
	case ovPane:
		return m.centerOver(screen, m.paneView())
	case ovQuestion:
		return m.centerOver(screen, m.questionView())
	case ovHistory:
		return m.centerOver(screen, m.historyView())
	}
	return screen
}

// centerOver floats a panel in the middle of the screen, over the live UI.
func (m Model) centerOver(screen, box string) string {
	lines := strings.Split(box, "\n")
	bw := 0
	for _, l := range lines {
		bw = max(bw, lipgloss.Width(l))
	}
	return floatOver(screen, box, max(0, (m.width-bw)/2), max(0, (m.height-len(lines))/2))
}

func (m Model) viewFooter() string {
	keys := "p find · n new · f fork · [ ] tabs · m model · d details · enter message · ? help"
	if m.focus == focusInput {
		keys = "enter send · alt+enter newline · ↑↓ history · ctrl+v image · esc done"
	}
	left := sMuted.Render(" " + keys)
	switch {
	case m.down:
		left = sRed.Render(" ● daemon unreachable") + sMuted.Render(" · "+m.hostKey+" · r retry")
	case m.loaded && (!m.connected || (m.connState != "" && m.connState != "connected")):
		left = sYellow.Render(" ● "+orDefault(m.connState, "connecting")+"…") + sMuted.Render(" · "+m.hostKey)
	}
	if m.status != "" && time.Since(m.statusT) < 8*time.Second {
		st := sGreen
		if m.isErr {
			st = sRed
		}
		left = st.Render(" " + m.status)
	}
	// the one action that matters right now, on the right
	right := ""
	if a := m.current(); a != nil && m.selSub == nil {
		switch {
		case len(m.perms[a.ID]) > 0:
			right = sGreen.Render("y allow") + sMuted.Render(" · ") + sRed.Render("x deny")
			if _, _, ok := m.pendingQuestion(a.ID); ok {
				right = sGreen.Render("enter answer") + sMuted.Render(" · ") + sRed.Render("x dismiss")
			}
		case a.Status == "running":
			right = sMuted.Render("s interrupt")
		case len(m.subagentsOf(a.ID)) > 0:
			right = sMuted.Render("u subagents")
		}
		if right != "" && m.focus == focusInput {
			right = sMuted.Render("esc, then ") + right
		}
	}
	if ps := m.runningPanes(); right == "" && len(ps) > 0 {
		// this workspace's hidden panes, and how many other workspaces have some open
		dir, elsewhere := m.workspaceDir(), 0
		for _, p := range ps {
			if p.dir != dir {
				elsewhere++
				continue
			}
			if right != "" {
				right += sMuted.Render(" · ")
			}
			right += sAccent.Render(paneKeys[p.kind]) + sMuted.Render(" "+p.name)
		}
		if elsewhere > 0 {
			if right != "" {
				right += sMuted.Render(" · ")
			}
			right += sMuted.Render(fmt.Sprintf("%d pane%s open elsewhere", elsewhere, map[bool]string{true: "", false: "s"}[elsewhere == 1]))
		}
	}
	if right == "" && m.updateAvail != "" {
		right = sBlue.Render("↑ "+m.updateAvail+" available") + sMuted.Render(" · "+m.updateHint)
	}
	left = truncate(left, max(10, m.width-lipgloss.Width(right)-3))
	return left + strings.Repeat(" ", max(1, m.width-lipgloss.Width(left)-lipgloss.Width(right)-1)) + right
}

func (m Model) viewMain(h int) string {
	w := m.mainW()
	v := m.wsViews[m.selWS]
	a := m.current()
	if v == nil {
		msg := sMuted.Render("Select a workspace (p to search), or press n to start an agent.")
		if !m.loaded {
			msg = m.spin.View() + sMuted.Render(" connecting to "+m.hostKey+"…")
		} else if m.down {
			msg = sRed.Render("Cannot reach the Paseo daemon.") + "\n\n" + sMuted.Render(truncate(m.downMsg, w-8))
			if m.downCode == "DAEMON_NOT_RUNNING" && m.local {
				msg += "\n\n" + sBold.Render("Press S to start it.")
			} else {
				msg += "\n\n" + sMuted.Render("Press r to retry.")
			}
		}
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, msg)
	}

	// workspace line: project › workspace, branch, PR, diff
	ws := v.ws
	wsParts := []string{sMuted.Render(ws.ProjectName()+" ›") + " " + sBold.Render(v.title())}
	if b := ws.Branch(); b != "" && b != ws.DisplayName() {
		wsParts = append(wsParts, sMuted.Render("⎇ "+b))
	} else if ws.IsWorktree() {
		wsParts = append(wsParts, sMuted.Render("⎇ worktree"))
	}
	if gh := ws.GithubRuntime; gh != nil && gh.PullRequest != nil {
		wsParts = append(wsParts, sBlue.Render(fmt.Sprintf("PR #%d", gh.PullRequest.Number)))
	}
	if d := ws.DiffStat; d != nil && (d.Additions > 0 || d.Deletions > 0) {
		wsParts = append(wsParts, sGreen.Render(fmt.Sprintf("+%d", d.Additions))+" "+sRed.Render(fmt.Sprintf("−%d", d.Deletions)))
	}
	wsLine := truncate(" "+strings.Join(wsParts, "  "), w)
	tabBar := m.viewTabs(v, w)

	if a == nil {
		msg := sMuted.Render("No agents in this workspace yet.") + "\n\n" + sBold.Render("Press n to start one here.")
		body := lipgloss.Place(w, max(1, h-2), lipgloss.Center, lipgloss.Center, msg)
		return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(wsLine + "\n" + tabBar + "\n" + body)
	}

	if ref := m.selSub; ref != nil {
		return m.viewSubTab(*ref, a, wsLine, tabBar, w, h)
	}

	// agent meta line for the focused tab
	var meta []string
	switch a.Status {
	case "running":
		st := m.spin.View() + sYellow.Render(" running")
		if t := m.turnStarted(*a); !t.IsZero() {
			st += sYellow.Render(" " + elapsed(t))
		}
		meta = append(meta, st)
	case "error":
		meta = append(meta, sRed.Render("error"))
	default:
		meta = append(meta, sGreen.Render(a.Status))
	}
	info := []string{a.Provider}
	if mode := deref(a.Snap.CurrentModeID); mode != "" {
		info = append(info, "mode "+mode)
	}
	info = append(info, "thinking "+a.Thinking)
	if u := a.Snap.LastUsage; u != nil {
		if u.ContextWindowMaxTokens > 0 {
			info = append(info, fmt.Sprintf("ctx %d%%", u.ContextWindowUsed*100/u.ContextWindowMaxTokens))
		}
		if u.TotalCostUsd > 0 {
			info = append(info, fmt.Sprintf("$%.2f", u.TotalCostUsd))
		}
	}
	info = append(info, a.ShortID)
	_ = info // the full agent status lives in the details panel (d)
	head := lipgloss.NewStyle().MaxWidth(w).Render(wsLine + "\n" + tabBar)

	vp := m.viewportView()
	if m.slash != nil && m.focus == focusInput {
		box := m.slashView(w)
		vp = floatOver(vp, box, 0, max(0, strings.Count(vp, "\n")+1-lipgloss.Height(box)))
	}
	parts := []string{head, vp}

	if perms := m.perms[a.ID]; len(perms) > 0 {
		p := perms[0]
		more := ""
		if len(perms) > 1 {
			more = sMuted.Render(fmt.Sprintf("  (+%d more)", len(perms)-1))
		}
		desc := permissionSummary(p)
		head := sRed.Bold(true).Render("⚠ Permission: ") + sBold.Render(p.Name) + more
		keys := sGreen.Render("y allow") + sMuted.Render(" · ") + sRed.Render("x deny")
		if qp, qs, ok := m.pendingQuestion(a.ID); ok {
			p, desc = qp, qs[0].Question
			if desc == p.Title {
				desc = p.Description // the options, e.g. "Red / Green / Blue"
			}
			head = sYellow.Bold(true).Render("? Question: ") + sBold.Render(orDefault(p.Title, orDefault(qs[0].Header, p.Name))) + more
			keys = sGreen.Render("enter answer") + sMuted.Render(" · ") + sRed.Render("x dismiss")
		}
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cRed).Width(w - 3).Render(
			head + "\n" + truncate(firstLine(desc), w-24) + "   " + keys)
		parts = append(parts, box)
	}

	// one status line: what the agent is doing, and its subagents
	parts = append(parts, m.composerStatus(*a, w))
	if att := m.viewAttachments(w); att != "" {
		parts = append(parts, att)
	}
	parts = append(parts, m.composerBox(m.input.View(), w))

	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(parts, "\n"))
}

// composerStatus is the line above the message box: activity on the left,
// subagents and scroll position on the right.
func (m Model) composerStatus(a Agent, w int) string {
	left := ""
	if a.Status == "running" {
		left = " " + m.activityLine(a)
	} else if len(m.perms[a.ID]) > 0 {
		left = " " + sRed.Bold(true).Render("! waiting for permission")
	}
	var right []string
	if !m.vp.AtBottom() {
		right = append(right, sMuted.Render(fmt.Sprintf("↓ %d%%  G to follow", int(m.vp.ScrollPercent()*100))))
	}
	if subs := m.subagentsOf(a.ID); len(subs) > 0 {
		right = append(right, m.subagentSummary(subs))
	}
	r := strings.Join(right, sMuted.Render("   "))
	if r != "" {
		r += " "
	}
	left = truncate(left, max(0, w-lipgloss.Width(r)-2))
	return left + strings.Repeat(" ", max(1, w-lipgloss.Width(left)-lipgloss.Width(r))) + r
}

// subagentSummary is the compact form of the subagent track: active states, then the total.
func (m Model) subagentSummary(subs []subagentEntry) string {
	counts := map[bucket]int{}
	for _, e := range subs {
		counts[e.bucket()]++
	}
	var segs []string
	for _, b := range []bucket{bNeedsInput, bFailed, bRunning, bAttention} {
		if n := counts[b]; n > 0 {
			segs = append(segs, m.bucketIcon(b, "")+" "+fmt.Sprintf("%d %s", n, strings.ToLower(bucketTitle[b])))
		}
	}
	total := fmt.Sprintf("%d subagent%s", len(subs), map[bool]string{true: "", false: "s"}[len(subs) == 1])
	if len(segs) == 0 {
		return sAccent.Render("⧉ ") + sMuted.Render(total)
	}
	return sAccent.Render("⧉ ") + strings.Join(segs, sMuted.Render(" · ")) + sMuted.Render(" of "+fmt.Sprint(len(subs)))
}

// composerBox draws the rounded frame around the message input.
func (m Model) composerBox(content string, w int) string {
	border := cBorder
	if m.focus == focusInput {
		border = cAccent
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(w - 2).Render(content)
}

// viewSubTab shows a provider subagent's transcript, read-only.
func (m Model) viewSubTab(ref subRef, parent *Agent, wsLine, tabBar string, w, h int) string {
	sub := m.subs[ref.ParentID][ref.SubID]
	b, status, kind := bDone, "", ""
	if sub != nil {
		b, status, kind = sub.bucket(), sub.Status, deref(sub.Title)
	}
	icon := m.bucketIcon(b, "")
	if b == bDone {
		icon = sGreen.Render("✓")
	}
	parentName := ref.ParentID
	if parent != nil {
		parentName = agentLabel(*parent)
	}
	meta := " " + icon + " " + status + sMuted.Render(" · subagent")
	if kind != "" {
		meta += sMuted.Render(" · " + kind)
	}
	meta += sMuted.Render(" · of " + parentName)
	head := wsLine + "\n" + tabBar
	note := sMuted.Render("Read-only subagent transcript · ctrl+w or × closes this tab")
	note += strings.Repeat("\n", max(0, m.input.Height()-1))
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(
		strings.Join([]string{head, m.viewportView(), truncate(meta, w), m.composerBox(note, w)}, "\n"))
}

// tabSpan is where a tab sits on the tab bar, for mouse clicks.
type tabSpan struct {
	start, end int // screen columns within the main pane
	closeAt    int // column of the × (−1 when the tab can't be closed)
	item       tabItem
}

var (
	cOnAccent = lipgloss.AdaptiveColor{Light: "#ffffff", Dark: "#14121f"}
	sTabSel   = lipgloss.NewStyle().Background(cAccent).Foreground(cOnAccent).Bold(true).Padding(0, 1)
	sTab      = lipgloss.NewStyle().Foreground(cMuted).Padding(0, 1)
)

// viewTabs is the workspace's tab bar: one tab per agent, like the desktop app.
func (m Model) viewTabs(v *wsView, w int) string {
	line, _ := m.tabLayout(v, w)
	return line
}

func (m Model) tabLayout(v *wsView, w int) (string, []tabSpan) {
	items := m.tabsOf(v.ws.ID)
	if len(items) == 0 {
		return sMuted.Render(" no tabs ") + sMuted.Foreground(cBorder).Render("─ ") + sMuted.Render("+ new ") +
			sMuted.Foreground(cBorder).Render("ctrl+n"), nil
	}
	type tab struct {
		s       string
		w       int
		closeAt int // offset of × inside the tab, or −1
		item    tabItem
	}
	var tabs []tab
	selIdx := 0
	cur := m.currentKey()
	maxLabel := max(10, min(32, (w-4)/max(1, len(items))-11))
	for i, it := range items {
		sel := it.key() == cur
		if sel {
			selIdx = i
		}
		var b bucket
		var name string
		closable := false
		if it.sub != nil {
			closable = true
			name = it.sub.SubID
			if sub := m.subs[it.sub.ParentID][it.sub.SubID]; sub != nil {
				b, name = sub.bucket(), sub.label()
			}
			name = "↳ " + truncate(name, maxLabel)
		} else if a := m.agentByID(it.agentID); a != nil {
			b, name = agentBucket(a.Snap), truncate(agentLabel(*a), maxLabel)
			if childOf(*a) != "" {
				closable = true
				name = "↳ " + name
			}
		}
		num := ""
		if i < 9 {
			num = fmt.Sprint(i+1) + " "
		}
		// on the highlighted tab, colours would fight the accent background,
		// so its state icon is drawn in the tab's own colour
		icon := m.bucketIcon(b, "")
		if b == bDone {
			icon = "•"
		}
		closeAt := -1
		var body string
		if sel {
			if b == bRunning {
				icon = ansi.Strip(m.spin.View())
			} else {
				icon = ansi.Strip(icon)
			}
			body = num + icon + " " + name
			if closable {
				closeAt = 1 + lipgloss.Width(body) + 1
				body += " ×"
			}
			body = sTabSel.Render(body)
		} else {
			inner := sMuted.Render(num) + icon + " " + sMuted.Render(name)
			if closable {
				closeAt = 1 + lipgloss.Width(inner) + 1 // padding, label, space
				inner += " " + sMuted.Render("×")
			}
			body = sTab.Render(inner)
		}
		tabs = append(tabs, tab{s: body, w: lipgloss.Width(body), closeAt: closeAt, item: it})
	}

	newTab := sMuted.Render(" + new ") + sMuted.Foreground(cBorder).Render("ctrl+n")
	if tabs[selIdx].closeAt >= 0 {
		newTab = sMuted.Render(" + new ") + sMuted.Foreground(cBorder).Render("ctrl+n · ctrl+w close")
	}
	budget := w - 3 - lipgloss.Width(newTab)

	// scroll the strip so the focused tab stays visible
	start := 0
	for start < selIdx {
		width := 0
		for i := start; i <= selIdx; i++ {
			width += tabs[i].w + 1
		}
		if width <= budget {
			break
		}
		start++
	}
	sep := sMuted.Foreground(cBorder).Render("│")
	line, col := " ", 1
	if start > 0 {
		line = sMuted.Render("‹")
	}
	var spans []tabSpan
	for i := start; i < len(tabs); i++ {
		if col+tabs[i].w+1 > budget+1 {
			line += sMuted.Render("›")
			col++
			break
		}
		sp := tabSpan{start: col, end: col + tabs[i].w, closeAt: -1, item: tabs[i].item}
		if tabs[i].closeAt >= 0 {
			sp.closeAt = col + tabs[i].closeAt
		}
		spans = append(spans, sp)
		line += tabs[i].s + sep
		col += tabs[i].w + 1
	}
	line += newTab
	// the focused agent's model and thinking level, right-aligned when there's room
	if badge := m.modelBadge(); badge != "" {
		if gap := w - lipgloss.Width(line) - lipgloss.Width(badge) - 1; gap >= 2 {
			line += strings.Repeat(" ", gap) + badge
		}
	}
	return line, spans
}

// modelBadge is "model · thinking level" of the focused agent tab, in the
// labels the provider lists when its models are loaded.
func (m Model) modelBadge() string {
	a := m.current()
	if a == nil || m.selSub != nil {
		return ""
	}
	id := currentModel(a)
	model, thinking := id, a.Thinking
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:] // "anthropic/claude-opus-5-5" -> "claude-opus-5-5"
	}
	for _, md := range m.modelCache[a.Snap.Provider] {
		if md.ID != id {
			continue
		}
		model = orDefault(md.Label, model)
		for _, o := range md.ThinkingOptions {
			if o.ID == thinking {
				thinking = orDefault(o.Label, thinking)
			}
		}
	}
	if model == "" || model == "default" {
		model = a.Snap.Provider
	}
	return sMuted.Render(model + " · " + strings.ToLower(thinking))
}

// closeTab closes a subagent tab (focused or not); agent tabs can't be closed.
func (m *Model) closeTab(t tabItem) tea.Cmd {
	if t.key() == m.currentKey() {
		return m.closeSubTab()
	}
	list := m.subTabs[m.selWS]
	for i, x := range list {
		if x.key() == t.key() {
			m.subTabs[m.selWS] = append(list[:i:i], list[i+1:]...)
		}
	}
	return nil
}

// clickTab handles mouse clicks on the tab bar (row 2 of the screen).
func (m *Model) clickTab(msg tea.MouseMsg) (bool, tea.Cmd) {
	v := m.wsViews[m.selWS]
	if v == nil || msg.Y != 1 || msg.Action != tea.MouseActionPress {
		return false, nil
	}
	x := msg.X - m.sidebarW()
	_, spans := m.tabLayout(v, m.mainW())
	for _, sp := range spans {
		if x < sp.start || x >= sp.end {
			continue
		}
		closable := sp.closeAt >= 0
		if closable && (msg.Button == tea.MouseButtonMiddle || (msg.Button == tea.MouseButtonLeft && x >= sp.closeAt-1)) {
			return true, m.closeTab(sp.item)
		}
		if msg.Button == tea.MouseButtonLeft {
			return true, m.selectItem(sp.item)
		}
		return true, nil
	}
	// "+ new" at the end of the bar
	if msg.Button == tea.MouseButtonLeft && len(spans) > 0 && x > spans[len(spans)-1].end && x < spans[len(spans)-1].end+8 {
		return true, func() tea.Msg { return openNewMsg{} }
	}
	return false, nil
}

type openNewMsg struct{}

// clickSidebar: click a workspace to open it, a project/status header to fold it; wheel scrolls.
func (m *Model) clickSidebar(msg tea.MouseMsg) (bool, tea.Cmd) {
	if m.state.HideSidebar || msg.X >= m.sideWidth() || msg.Y < 0 || msg.Y >= m.height-1 {
		return false, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.sideScroll = max(0, m.sidebarStart(m.height-1-len(m.sidebarHead()))-1)
		return true, nil
	case tea.MouseButtonWheelDown:
		m.sideScroll = min(m.sidebarStart(m.height-1-len(m.sidebarHead()))+1, max(0, len(m.rows)-1))
		return true, nil
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return true, nil
		}
		i := m.rowAtY(msg.Y)
		if i < 0 {
			return true, nil
		}
		keep := m.sideScroll
		m.focus = focusList
		m.input.Blur()
		switch r := m.rows[i]; r.kind {
		case rowProject:
			m.cursor = i
			m.setCollapsed(r.key, !m.state.Collapsed[r.key])
			m.sideScroll = keep
			return true, nil
		case rowWorkspace:
			cmd := m.moveCursor(i)
			m.sideScroll = keep // a click shouldn't make the list jump
			return true, cmd
		}
		return true, nil
	}
	return true, nil
}

// clickMain: the subagent track opens the list; the message box takes focus.
func (m *Model) clickMain(msg tea.MouseMsg) (bool, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress || msg.X < m.sidebarW() || msg.X >= m.sidebarW()+m.mainW() {
		return false, nil
	}
	a := m.current()
	if a == nil || m.selSub != nil {
		return false, nil
	}
	boxTop := m.height - 1 /*footer*/ - 2 - m.input.Height()
	if msg.Y >= boxTop && msg.Y < m.height-1 {
		m.sel = selection{}
		m.focus = focusInput
		return true, m.input.Focus()
	}
	statusY := boxTop - m.attachHeight() - 1
	if msg.Y == statusY && len(m.subagentsOf(a.ID)) > 0 {
		m.focus = focusList
		m.input.Blur()
		mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
		*m = mm.(Model)
		return true, cmd
	}
	return false, nil
}

func (m Model) dividerColor(t dragTarget) lipgloss.TerminalColor {
	if m.drag == t {
		return cAccent
	}
	return cBorder
}

func orDefault(s, d string) string {
	if s == "" || s == "disposed" {
		return d
	}
	return s
}

func shortHome(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, h) {
		return "~" + p[len(h):]
	}
	return p
}

func helpBox() string {
	rows := [][2]string{
		{"p, ctrl+p", "command center: workspaces, agents, actions"},
		{"J / K, ↑ / ↓", "move through projects and workspaces"},
		{"{ / }", "previous / next workspace in the tree (works with the sidebar hidden)"},
		{"h / l, ← / →", "collapse / expand project"},
		{"space", "toggle project"},
		{"z", "collapse all other projects"},
		{"o", "group sidebar by project / by status"},
		{"b, ctrl+b", "hide / show the sidebar"},
		{"< / >", "move the sidebar divider (narrower / wider) — or drag it"},
		{", / .", "move the details-panel divider (wider / narrower) — or drag it"},
		{"d", "agent details panel (status, model, context, cost, tasks, subagents)"},
		{"m", "agent settings: model, thinking, mode, Fast"},
		{"alt+↑ / alt+↓", "scroll the details panel (or use the mouse wheel over it)"},
		{"[ / ], tab / shift+tab", "previous / next agent tab"},
		{"alt+1 … alt+9", "jump to agent tab"},
		{"u", "subagents of this agent (open one as a ↳ tab)"},
		{"w, ctrl+w, middle-click", "close a subagent tab (or click its ×)"},
		{"click a tab", "switch to it"},
		{"enter, i", "write a message to the agent"},
		{"/", "commands and skills of the agent (also typed in the message box)"},
		{"ctrl+v", "paste an image (or paste/drop an image path)"},
		{"n, ctrl+n", "new agent (in this workspace, or a new one)"},
		{"N", "new workspace: project, project directory or new worktree, start branch, model"},
		{"↑ / ↓ in the input", "previous / next prompt from history"},
		{"mouse drag", "select transcript text (copied on release)"},
		{"s", "interrupt running agent"},
		{"y / x", "allow / deny pending permission"},
		{"enter (question)", "answer the agent's question; x dismisses it"},
		{"a", "archive agent / workspace, or remove project"},
		{"D", "delete agent"},
		{"f", "fork the agent: a new tab (or workspace) that starts from its chat history"},
		{"R", "rename the agent (its tab)"},
		{"H", "history: every agent, archived ones too; enter opens or restores one"},
		{"e", "edit the workspace in $VISUAL / $EDITOR, in a floating pane (ctrl+\\ hides it)"},
		{"F", "browse files in ranger / yazi / lf / nnn in a floating pane (else Finder)"},
		{"A", "toggle archived agents"},
		{"j / k", "scroll transcript"},
		{"pgup/pgdn, ctrl+u/d", "scroll transcript by page / half page"},
		{"g / G", "transcript top / bottom (follow)"},
		{"t", "show / hide thinking"},
		{"v", "verbose tool output (commands, diffs)"},
		{"r", "reload transcript / reconnect"},
		{"S", "start local daemon (when stopped)"},
		{"q, ctrl+c", "quit (asks first; y or the same key again confirms)"},
	}
	var b strings.Builder
	b.WriteString(sAccent.Render("paseo-tui") + sMuted.Render("  keys") + "\n\n")
	for _, r := range rows {
		b.WriteString(sBold.Render(fmt.Sprintf("%-22s", r[0])) + sMuted.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + sMuted.Render("legend: ") + sRed.Bold(true).Render("!") + sMuted.Render(" needs input  ") + sRed.Render("✗") +
		sMuted.Render(" failed  ") + sYellow.Render("⠋") + sMuted.Render(" working  ") + sBlue.Render("◆") + sMuted.Render(" ready to review  ") +
		sMuted.Render("· ⎇ done"))
	b.WriteString("\n\n" + sMuted.Render("any key to close"))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(1, 3).Render(b.String())
}
