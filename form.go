package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// New workspace / new agent (n, N), modelled on the desktop's New workspace
// screen: a row of pickers (project · where · from · provider · model ·
// thinking) above a composer. The workspace and its first agent are created in
// one call, so the daemon names the workspace and branch after the prompt.

const (
	isoThis     = iota // a new tab in the open workspace
	isoLocal           // a workspace in the project directory
	isoWorktree        // a new git worktree
)

var isoLabels = map[int]string{isoThis: "This workspace", isoLocal: "Project directory", isoWorktree: "New worktree"}

const (
	chipProject = iota
	chipWhere
	chipFrom
	chipProvider
	chipModel
	chipThinking
)

type projOpt struct {
	id, name, root string
	git            bool
}

type pickItem struct{ id, label, hint string }

type NewAgentForm struct {
	prompt textarea.Model

	projects []projOpt
	projIdx  int

	// the workspace that was open, for "This workspace"
	wsID, wsName, wsDir, wsProject string

	iso       int
	preferIso int // remembered isolation for new workspaces (local or worktree)

	branches map[string][]string // project root -> branches
	ref      string              // "" = the project's current HEAD

	providers []Provider
	provider  string
	models    map[string][]ModelInfo
	model     string // "" = provider default
	thinking  string // "" = model default

	focus int // -1 = prompt, else a chip

	// open picker list
	picking   bool
	pickChip  int
	filter    textinput.Model
	pickAll   []pickItem
	pickShown []pickItem
	pickSel   int

	attachments []string
	fork        *forkSource // set when forking another agent (f)
	pasting     bool
	width       int
	height      int
	err         string
	busy        bool
}

type branchesMsg struct {
	root     string
	branches []string
	err      error
}

// forkSource is the agent a fork starts from. Like the desktop's "Fork in new
// tab", its chat history goes along as an attachment of the first prompt, and
// the new agent starts with the same provider, model, thinking, mode and options.
type forkSource struct {
	agentID, name, provider, cwd, modeID string
	options                              json.RawMessage
	context                              json.RawMessage // the chat-history attachment; nil while loading
	items                                int
	err                                  string
}

type forkContextMsg struct {
	src     *forkSource
	context json.RawMessage
	items   int
	err     error
}

func newPrompt() textarea.Model {
	p := textarea.New()
	p.Placeholder = "What should the agent do?"
	p.ShowLineNumbers = false
	p.Prompt = ""
	p.SetHeight(4)
	p.CharLimit = 0
	p.FocusedStyle.CursorLine = lipgloss.NewStyle()
	p.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")
	p.Focus()
	return p
}

func (f *NewAgentForm) Init() tea.Cmd { return textarea.Blink }

func (f *NewAgentForm) SetSize(w, h int) {
	f.width, f.height = min(112, w-4), h
	f.prompt.SetWidth(f.width - 8)
}

func (f *NewAgentForm) project() *projOpt {
	if f.projIdx >= 0 && f.projIdx < len(f.projects) {
		return &f.projects[f.projIdx]
	}
	return nil
}

func (f *NewAgentForm) canThis() bool {
	p := f.project()
	return f.wsID != "" && p != nil && p.id == f.wsProject
}

func (f *NewAgentForm) canWorktree() bool {
	p := f.project()
	return p != nil && p.git
}

// fixIso keeps the isolation valid for the chosen project; a preferred
// worktree comes back when switching to a git project.
func (f *NewAgentForm) fixIso() {
	if f.iso == isoThis && !f.canThis() {
		f.iso = f.preferIso
	}
	if f.iso == isoLocal && f.preferIso == isoWorktree && f.canWorktree() {
		f.iso = isoWorktree
	}
	if f.iso == isoWorktree && !f.canWorktree() {
		f.iso = isoLocal
	}
}

func (f *NewAgentForm) SetProviders(provs []Provider) {
	f.providers = f.providers[:0]
	for _, p := range provs {
		if strings.EqualFold(p.Enabled, "enabled") || p.Enabled == "" {
			f.providers = append(f.providers, p)
		}
	}
	if f.provider == "" && len(f.providers) > 0 {
		f.provider = f.providers[0].Provider
	}
}

func (f *NewAgentForm) modelInfo() *ModelInfo {
	for i, md := range f.models[f.provider] {
		if md.ID == f.model {
			return &f.models[f.provider][i]
		}
	}
	return nil
}

// chips visible for the current choices ("from" only applies to new worktrees).
func (f *NewAgentForm) chips() []int {
	out := []int{chipProject, chipWhere}
	if f.iso == isoWorktree {
		out = append(out, chipFrom)
	}
	return append(out, chipProvider, chipModel, chipThinking)
}

func (f *NewAgentForm) moveFocus(dir int) tea.Cmd {
	order := append([]int{-1}, f.chips()...)
	i := 0
	for j, c := range order {
		if c == f.focus {
			i = j
		}
	}
	f.focus = order[(i+dir+len(order))%len(order)]
	if f.focus == -1 {
		return f.prompt.Focus()
	}
	f.prompt.Blur()
	return nil
}

// ---------- picker lists ----------

func (f *NewAgentForm) itemsFor(chip int) (items []pickItem, cur string) {
	switch chip {
	case chipProject:
		for _, p := range f.projects {
			items = append(items, pickItem{id: p.id, label: p.name, hint: shortHome(p.root)})
		}
		items = append(items, pickItem{id: "+add", label: "+ Add project…", hint: "existing folder, new folder or clone"})
		if p := f.project(); p != nil {
			cur = p.id
		}
	case chipWhere:
		if f.canThis() {
			items = append(items, pickItem{id: "0", label: isoLabels[isoThis], hint: "new tab in " + truncate(f.wsName, 30)})
		}
		items = append(items, pickItem{id: "1", label: isoLabels[isoLocal], hint: "work in the project folder"})
		if f.canWorktree() {
			items = append(items, pickItem{id: "2", label: isoLabels[isoWorktree], hint: "isolated git worktree"})
		}
		cur = fmt.Sprint(f.iso)
	case chipFrom:
		items = append(items, pickItem{id: "", label: "current branch", hint: "the project directory's HEAD"})
		if p := f.project(); p != nil {
			for _, b := range f.branches[p.root] {
				items = append(items, pickItem{id: b, label: b})
			}
		}
		cur = f.ref
	case chipProvider:
		for _, p := range f.providers {
			hint := p.Provider
			if p.Status != "" && p.Status != "available" {
				hint += " · " + p.Status
			}
			items = append(items, pickItem{id: p.Provider, label: orDefault(p.Label, p.Provider), hint: hint})
		}
		cur = f.provider
	case chipModel:
		items = append(items, pickItem{id: "", label: "default", hint: "the provider's default model"})
		for _, md := range f.models[f.provider] {
			items = append(items, pickItem{id: md.ID, label: md.Label, hint: md.ID})
		}
		cur = f.model
	case chipThinking:
		items = append(items, pickItem{id: "", label: "default"})
		if md := f.modelInfo(); md != nil {
			for _, o := range md.ThinkingOptions {
				items = append(items, pickItem{id: o.ID, label: o.Label, hint: o.Description})
			}
		}
		cur = f.thinking
	}
	return items, cur
}

// choose applies a picked value; it returns "add-project" when that was picked.
func (f *NewAgentForm) choose(chip int, id string) string {
	switch chip {
	case chipProject:
		if id == "+add" {
			return "add-project"
		}
		for i, p := range f.projects {
			if p.id == id {
				f.projIdx = i
			}
		}
		f.fixIso()
		f.ref = ""
	case chipWhere:
		fmt.Sscan(id, &f.iso)
		if f.iso != isoThis {
			f.preferIso = f.iso
		}
	case chipFrom:
		f.ref = id
	case chipProvider:
		if id != f.provider {
			f.provider, f.model, f.thinking = id, "", ""
		}
	case chipModel:
		if id != f.model {
			f.model, f.thinking = id, ""
		}
	case chipThinking:
		f.thinking = id
	}
	return ""
}

// cycle steps a chip's value with ←/→ without opening the list.
func (f *NewAgentForm) cycle(chip, dir int) string {
	items, cur := f.itemsFor(chip)
	if chip == chipProject && len(items) > 0 {
		items = items[:len(items)-1] // cycling never lands on "+ Add project…"
	}
	if len(items) == 0 {
		return ""
	}
	i := 0
	for j, it := range items {
		if it.id == cur {
			i = j
		}
	}
	return f.choose(chip, items[(i+dir+len(items))%len(items)].id)
}

func (f *NewAgentForm) openPicker(chip int) tea.Cmd {
	f.picking, f.pickChip, f.pickSel = true, chip, 0
	f.pickAll, _ = f.itemsFor(chip)
	ti := textinput.New()
	ti.Placeholder = "type to filter…"
	ti.Prompt = ""
	f.filter = ti
	f.refilter()
	_, cur := f.itemsFor(chip)
	for i, it := range f.pickShown {
		if it.id == cur {
			f.pickSel = i
		}
	}
	return f.filter.Focus()
}

func (f *NewAgentForm) refilter() {
	q := strings.ToLower(strings.TrimSpace(f.filter.Value()))
	f.pickShown = f.pickShown[:0]
	for _, it := range f.pickAll {
		if q == "" {
			f.pickShown = append(f.pickShown, it)
		} else if _, ok := fuzzyScore(q, strings.ToLower(it.label+" "+it.hint)); ok {
			f.pickShown = append(f.pickShown, it)
		}
	}
	f.pickSel = min(f.pickSel, max(0, len(f.pickShown)-1))
}

// ---------- keys ----------

// Update returns (done, submit, action, cmd); action is "add-project" when the
// user picked "+ Add project…".
func (f *NewAgentForm) Update(k tea.KeyMsg) (bool, bool, string, tea.Cmd) {
	if f.busy {
		return false, false, "", nil
	}
	if f.picking {
		switch k.String() {
		case "esc":
			f.picking = false
			return false, false, "", nil
		case "up", "ctrl+p", "ctrl+k":
			f.pickSel = max(0, f.pickSel-1)
			return false, false, "", nil
		case "down", "ctrl+n", "ctrl+j", "tab":
			f.pickSel = min(len(f.pickShown)-1, f.pickSel+1)
			return false, false, "", nil
		case "enter":
			f.picking = false
			if f.pickSel < len(f.pickShown) {
				return false, false, f.choose(f.pickChip, f.pickShown[f.pickSel].id), nil
			}
			return false, false, "", nil
		}
		var c tea.Cmd
		f.filter, c = f.filter.Update(k)
		f.refilter()
		return false, false, "", c
	}

	switch k.String() {
	case "esc":
		return true, false, "", nil
	case "tab":
		return false, false, "", f.moveFocus(1)
	case "shift+tab":
		return false, false, "", f.moveFocus(-1)
	case "ctrl+s":
		return f.trySubmit()
	}

	if f.focus >= 0 { // a picker chip
		switch k.String() {
		case "left", "h":
			return false, false, f.cycle(f.focus, -1), nil
		case "right", "l":
			return false, false, f.cycle(f.focus, 1), nil
		case "enter", " ", "up":
			return false, false, "", f.openPicker(f.focus)
		case "down":
			f.focus = -1
			return false, false, "", f.prompt.Focus()
		}
		return false, false, "", nil
	}

	// the composer
	if k.Paste {
		if paths := imagePathsIn(string(k.Runes)); len(paths) > 0 {
			f.attachments = append(f.attachments, paths...)
			return false, false, "", nil
		}
	}
	switch k.String() {
	case "enter":
		return f.trySubmit()
	case "ctrl+v":
		if !f.pasting {
			f.pasting = true
			return false, false, "", readClipboardImage
		}
		f.pasting = false // second pass: the fallback text paste
	case "backspace":
		if f.prompt.Value() == "" && len(f.attachments) > 0 {
			f.attachments = f.attachments[:len(f.attachments)-1]
			return false, false, "", nil
		}
	case "up":
		if f.prompt.Line() == 0 {
			f.focus = f.chips()[0]
			f.prompt.Blur()
			return false, false, "", nil
		}
	}
	var c tea.Cmd
	f.prompt, c = f.prompt.Update(k)
	return false, false, "", c
}

func (f *NewAgentForm) trySubmit() (bool, bool, string, tea.Cmd) {
	switch {
	case strings.TrimSpace(f.prompt.Value()) == "":
		f.err = "Write a prompt first."
	case f.project() == nil && f.iso != isoThis:
		f.err = "Choose a project."
	case f.provider == "":
		f.err = "Choose a provider."
	case f.fork != nil && f.fork.err != "":
		f.err = "Chat history: " + f.fork.err
	case f.fork != nil && f.fork.context == nil:
		f.err = "Still loading the chat history…"
	default:
		return true, true, "", nil
	}
	f.focus = -1
	return false, false, "", f.prompt.Focus()
}

var slugWords = [][]string{
	{"brave", "calm", "eager", "fancy", "gentle", "happy", "jolly", "keen", "lively", "merry", "nimble", "proud", "quiet", "rapid", "sunny", "swift", "tidy", "witty", "bold", "clever"},
	{"otter", "falcon", "badger", "heron", "lynx", "panda", "raven", "tiger", "walrus", "yak", "koala", "lemur", "moose", "newt", "orca", "puffin", "quail", "seal", "trout", "wolf"},
}

// Request is the bridge call that creates the agent (and its workspace).
func (f *NewAgentForm) Request() (op string, fields map[string]any) {
	agent := map[string]any{
		"provider":      f.provider,
		"initialPrompt": strings.TrimSpace(f.prompt.Value()),
	}
	if f.model != "" {
		agent["model"] = f.model
	}
	if f.thinking != "" {
		agent["thinkingOptionId"] = f.thinking
	}
	if len(f.attachments) > 0 {
		agent["images"] = f.attachments
	}
	if fk := f.fork; fk != nil {
		agent["attachments"] = []json.RawMessage{fk.context}
		if f.provider == fk.provider { // the source's mode and options only mean something to its provider
			if fk.modeID != "" {
				agent["modeId"] = fk.modeID
			}
			if len(fk.options) > 0 && string(fk.options) != "null" {
				agent["providerOptions"] = fk.options
			}
		}
	}
	if f.iso == isoThis {
		agent["cwd"] = f.wsDir
		if f.fork != nil && f.fork.cwd != "" {
			agent["cwd"] = f.fork.cwd
		}
		return "createAgent", map[string]any{"workspaceId": f.wsID, "agent": agent}
	}
	p := f.project()
	agent["cwd"] = p.root
	var source map[string]any
	if f.iso == isoWorktree {
		source = map[string]any{
			"kind": "worktree", "cwd": p.root, "projectId": p.id,
			"worktreeSlug": slugWords[0][rand.Intn(len(slugWords[0]))] + "-" + slugWords[1][rand.Intn(len(slugWords[1]))],
		}
		if f.ref != "" {
			source["action"], source["refName"] = "branch-off", f.ref
		}
	} else {
		source = map[string]any{"kind": "directory", "path": p.root, "projectId": p.id}
	}
	return "createWorkspace", map[string]any{"source": source, "agent": agent}
}

// ---------- view ----------

func (f *NewAgentForm) chipValue(chip int) string {
	switch chip {
	case chipProject:
		if p := f.project(); p != nil {
			return p.name
		}
		return "choose project"
	case chipWhere:
		return isoLabels[f.iso]
	case chipFrom:
		if f.ref == "" {
			return "from current branch"
		}
		return "from " + f.ref
	case chipProvider:
		for _, p := range f.providers {
			if p.Provider == f.provider {
				return orDefault(p.Label, p.Provider)
			}
		}
		return orDefault(f.provider, "provider")
	case chipModel:
		if md := f.modelInfo(); md != nil {
			return md.Label
		}
		if f.model != "" {
			return f.model
		}
		return "default model"
	case chipThinking:
		if md := f.modelInfo(); md != nil {
			for _, o := range md.ThinkingOptions {
				if o.ID == f.thinking {
					return "thinking " + o.Label
				}
			}
		}
		if f.thinking != "" {
			return "thinking " + f.thinking
		}
		return "thinking default"
	}
	return ""
}

func (f *NewAgentForm) View(m Model) string {
	w := f.width
	inner := w - 4
	title := "New workspace"
	if f.iso == isoThis {
		title = "New agent in " + truncate(f.wsName, inner-16)
	}
	if fk := f.fork; fk != nil {
		title = fmt.Sprintf("Fork %q into a new workspace", truncate(fk.name, inner-34))
		if f.iso == isoThis {
			title = fmt.Sprintf("Fork %q into a new tab", truncate(fk.name, inner-28))
		}
	}
	lines := []string{sAccent.Render(" " + title), ""}

	var chips []string
	for _, c := range f.chips() {
		v := truncate(f.chipValue(c), 26)
		st := lipgloss.NewStyle().Foreground(cMuted).Padding(0, 1)
		if f.focus == c {
			st = lipgloss.NewStyle().Background(cAccent).Foreground(cOnAccent).Bold(true).Padding(0, 1)
		}
		chips = append(chips, st.Render(v+" ▾"))
	}
	lines = append(lines, wrapChips(chips, inner))

	if f.picking {
		lines = append(lines, f.pickerView(inner))
	}

	border := cBorder
	if f.focus == -1 && !f.picking {
		border = cAccent
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1).Width(inner - 2).Render(f.prompt.View())
	lines = append(lines, "", box)
	if len(f.attachments) > 0 {
		var names []string
		for _, a := range f.attachments {
			names = append(names, "▣ "+filepath.Base(a))
		}
		lines = append(lines, sAccent.Render(" "+truncate(strings.Join(names, "  "), inner-2)))
	}
	if fk := f.fork; fk != nil {
		switch {
		case fk.err != "":
			lines = append(lines, sRed.Render(" ⑂ chat history: "+truncate(fk.err, inner-20)))
		case fk.context == nil:
			lines = append(lines, " "+m.spin.View()+sMuted.Render(" loading the chat history…"))
		default:
			lines = append(lines, sAccent.Render(" ⑂ ")+sMuted.Render(fmt.Sprintf("chat history from %q · %d items", truncate(fk.name, inner-40), fk.items)))
		}
	}
	if f.busy {
		lines = append(lines, " "+m.spin.View()+sMuted.Render(" creating…"))
	}
	if f.err != "" {
		lines = append(lines, sRed.Render(" "+f.err))
	}
	help := "enter start · alt+enter newline · ctrl+v image · ↑/tab pickers · esc cancel"
	switch {
	case f.picking:
		help = "type to filter · ↑↓ move · enter choose · esc close list"
	case f.focus >= 0:
		help = "←/→ change · enter list · tab next · ↓ prompt · esc cancel"
	}
	lines = append(lines, "", sMuted.Render(" "+help))
	return frame(lines, w)
}

func wrapChips(chips []string, width int) string {
	var rows []string
	row := ""
	for _, c := range chips {
		if row != "" && lipgloss.Width(row)+1+lipgloss.Width(c) > width {
			rows = append(rows, row)
			row = ""
		}
		if row != "" {
			row += " "
		}
		row += c
	}
	return strings.Join(append(rows, row), "\n")
}

func (f *NewAgentForm) pickerView(inner int) string {
	names := map[int]string{chipProject: "Project", chipWhere: "Where", chipFrom: "Start from", chipProvider: "Provider", chipModel: "Model", chipThinking: "Thinking"}
	lines := []string{"", sAccent.Render(" "+names[f.pickChip]+" › ") + f.filter.View()}
	budget := max(4, min(10, f.height-24))
	start := 0
	if f.pickSel >= budget {
		start = f.pickSel - budget + 1
	}
	for i := start; i < len(f.pickShown) && i < start+budget; i++ {
		it := f.pickShown[i]
		if i == f.pickSel {
			lines = append(lines, fillBg(rightAlign(sAccent.Render(" ▌ ")+sBold.Render(it.label), sMuted.Render(truncate(it.hint, inner/2)), inner), inner, cSelBg))
		} else {
			lines = append(lines, rightAlign("   "+it.label, sMuted.Render(truncate(it.hint, inner/2)), inner))
		}
	}
	if len(f.pickShown) == 0 {
		lines = append(lines, sMuted.Render("   no matches"))
	}
	return strings.Join(lines, "\n")
}
