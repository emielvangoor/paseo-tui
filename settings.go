package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Agent settings (m): model, thinking level, mode and provider toggles of the
// focused agent, applied immediately through the daemon.

type ModelInfo struct {
	ID              string           `json:"id"`
	Label           string           `json:"label"`
	Description     string           `json:"description"`
	ThinkingOptions []ThinkingOption `json:"thinkingOptions"`
}

type ThinkingOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	IsDefault   bool   `json:"isDefault"`
}

type AgentMode struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type AgentFeature struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Label string          `json:"label"`
	Value json.RawMessage `json:"value"`
}

func (f AgentFeature) on() bool { return string(f.Value) == "true" }

type modelsMsg struct {
	provider string
	models   []ModelInfo
	err      error
}

const (
	setModel = iota
	setThinking
	setMode
	setFeatures // first feature row; one row per toggle
)

type Settings struct {
	agentID, agentName, provider string
	models                       []ModelInfo
	loading                      bool
	err                          string
	row                          int

	picking bool // model search list open
	filter  textinput.Model
	matches []int
	sel     int
	width   int
	height  int
}

func (m *Model) openSettings() (tea.Model, tea.Cmd) {
	a := m.current()
	if a == nil || m.selSub != nil || m.br == nil {
		return *m, nil
	}
	ti := textinput.New()
	ti.Placeholder = "search models…"
	ti.Prompt = "  "
	ti.PromptStyle = sAccent
	st := &Settings{agentID: a.ID, agentName: agentLabel(*a), provider: a.Snap.Provider, filter: ti,
		width: m.width, height: m.height, loading: true}
	if cached, ok := m.modelCache[st.provider]; ok {
		st.models, st.loading = cached, false
	}
	m.settings = st
	m.overlay = ovSettings
	if !st.loading {
		return *m, nil
	}
	br, prov := m.br, st.provider
	return *m, func() tea.Msg {
		data, err := br.Call("models", map[string]any{"provider": prov}, 30*time.Second)
		var res struct {
			Models []ModelInfo `json:"models"`
			Error  *string     `json:"error"`
		}
		if err == nil {
			err = json.Unmarshal(data, &res)
			if err == nil && res.Error != nil && *res.Error != "" {
				err = fmt.Errorf("%s", *res.Error)
			}
		}
		return modelsMsg{provider: prov, models: res.Models, err: err}
	}
}

// currentModel is the agent's model id as the provider names it.
func currentModel(a *Agent) string {
	if a == nil {
		return ""
	}
	if r := a.Snap.RuntimeInfo; r != nil && r.Model != nil && *r.Model != "" {
		return *r.Model
	}
	return deref(a.Snap.Model)
}

func (st *Settings) modelByID(id string) *ModelInfo {
	for i := range st.models {
		if st.models[i].ID == id {
			return &st.models[i]
		}
	}
	return nil
}

func (st *Settings) refilter() {
	q := strings.ToLower(strings.TrimSpace(st.filter.Value()))
	st.matches = st.matches[:0]
	for i, md := range st.models {
		if q == "" {
			st.matches = append(st.matches, i)
			continue
		}
		if _, ok := fuzzyScore(q, strings.ToLower(md.Label+" "+md.ID)); ok {
			st.matches = append(st.matches, i)
		}
	}
	st.sel = min(st.sel, max(0, len(st.matches)-1))
}

// settingsKey handles keys while the settings panel is open.
func (m *Model) settingsKey(k tea.KeyMsg) tea.Cmd {
	st := m.settings
	a := m.agentByID(st.agentID)
	if a == nil {
		m.overlay, m.settings = ovNone, nil
		return nil
	}

	if st.picking {
		switch k.String() {
		case "esc":
			st.picking = false
			return nil
		case "up", "ctrl+p", "ctrl+k":
			st.sel = max(0, st.sel-1)
			return nil
		case "down", "ctrl+n", "ctrl+j", "tab":
			st.sel = min(len(st.matches)-1, st.sel+1)
			return nil
		case "enter":
			st.picking = false
			if st.sel < len(st.matches) {
				md := st.models[st.matches[st.sel]]
				if md.ID != currentModel(a) {
					return m.applySetting("Model → "+md.Label, "setModel", map[string]any{"modelId": md.ID})
				}
			}
			return nil
		}
		var c tea.Cmd
		st.filter, c = st.filter.Update(k)
		st.refilter()
		return c
	}

	rows := setFeatures + len(m.toggles(a))
	switch k.String() {
	case "esc", "q", "m":
		m.overlay, m.settings = ovNone, nil
	case "up", "k", "shift+tab":
		st.row = (st.row - 1 + rows) % rows
	case "down", "j", "tab":
		st.row = (st.row + 1) % rows
	case "enter":
		if st.row == setModel {
			return m.openModelPicker(a)
		}
		return m.cycleSetting(a, 1)
	case "left", "h":
		if st.row == setModel {
			return m.openModelPicker(a)
		}
		return m.cycleSetting(a, -1)
	case "right", "l", " ":
		if st.row == setModel {
			return m.openModelPicker(a)
		}
		return m.cycleSetting(a, 1)
	}
	return nil
}

func (m *Model) openModelPicker(a *Agent) tea.Cmd {
	st := m.settings
	if len(st.models) == 0 {
		return nil
	}
	st.picking = true
	st.filter.SetValue("")
	st.refilter()
	for i, idx := range st.matches {
		if st.models[idx].ID == currentModel(a) {
			st.sel = i
		}
	}
	return st.filter.Focus()
}

func (m *Model) toggles(a *Agent) []AgentFeature {
	var out []AgentFeature
	for _, f := range a.Snap.Features {
		if f.Type == "toggle" {
			out = append(out, f)
		}
	}
	return out
}

func (st *Settings) thinkingOptions(a *Agent) []ThinkingOption {
	if md := st.modelByID(currentModel(a)); md != nil {
		return md.ThinkingOptions
	}
	return nil
}

func (m *Model) cycleSetting(a *Agent, dir int) tea.Cmd {
	st := m.settings
	switch {
	case st.row == setThinking:
		opts := st.thinkingOptions(a)
		if len(opts) == 0 {
			return nil
		}
		cur := deref(a.Snap.ThinkingOptionID)
		if cur == "" {
			cur = a.Thinking
		}
		i := 0
		for j, o := range opts {
			if o.ID == cur {
				i = j
			}
		}
		o := opts[(i+dir+len(opts))%len(opts)]
		return m.applySetting("Thinking → "+o.Label, "setThinking", map[string]any{"optionId": o.ID})
	case st.row == setMode:
		modes := a.Snap.AvailableModes
		if len(modes) == 0 {
			return nil
		}
		cur, i := deref(a.Snap.CurrentModeID), 0
		for j, md := range modes {
			if md.ID == cur {
				i = j
			}
		}
		md := modes[(i+dir+len(modes))%len(modes)]
		return m.applySetting("Mode → "+orDefault(md.Label, md.ID), "setMode", map[string]any{"modeId": md.ID})
	case st.row >= setFeatures:
		tg := m.toggles(a)
		if i := st.row - setFeatures; i < len(tg) {
			f := tg[i]
			return m.applySetting(fmt.Sprintf("%s → %s", f.Label, map[bool]string{true: "on", false: "off"}[!f.on()]),
				"setFeature", map[string]any{"featureId": f.ID, "value": !f.on()})
		}
	}
	return nil
}

func (m *Model) applySetting(what, op string, fields map[string]any) tea.Cmd {
	fields["agentId"] = m.settings.agentID
	m.setStatus(what+"…", false)
	return m.call(what, op, fields)
}

// ---------- view ----------

func (m Model) settingsView() string {
	st := m.settings
	a := m.agentByID(st.agentID)
	w := max(50, min(80, st.width-8))
	inner := w - 4
	lines := []string{sAccent.Render(" Agent settings") + sMuted.Render(" · "+truncate(st.agentName, inner-18)), ""}
	if a == nil {
		return ""
	}

	if st.picking {
		lines = append(lines, st.filter.View(), sMuted.Foreground(cBorder).Render(strings.Repeat("─", inner)))
		budget := max(4, min(14, st.height-12))
		start := 0
		if st.sel >= budget {
			start = st.sel - budget + 1
		}
		cur := currentModel(a)
		for i := start; i < len(st.matches) && i < start+budget; i++ {
			md := st.models[st.matches[i]]
			name := md.Label
			if md.ID == cur {
				name += sGreen.Render("  ✓ current")
			}
			line := "  " + name
			if i == st.sel {
				line = sAccent.Render("▌ ") + sBold.Render(md.Label)
				if md.ID == cur {
					line += sGreen.Render("  ✓ current")
				}
			}
			line = rightAlign(line, sMuted.Render(truncate(md.ID, inner/2)), inner)
			if i == st.sel {
				line = fillBg(line, inner, cSelBg)
			}
			lines = append(lines, line)
		}
		if len(st.matches) == 0 {
			lines = append(lines, sMuted.Render("  no models match"))
		}
		lines = append(lines, "", sMuted.Render("  ↑↓ move · enter switch model · esc back"))
		return frame(lines, w)
	}

	row := func(i int, label, value, hint string) {
		mark := "  "
		lst := sMuted
		if st.row == i {
			mark, lst = sAccent.Render("▸ "), sAccent
		}
		lines = append(lines, rightAlign(mark+lst.Render(fmt.Sprintf("%-10s", label))+value, sMuted.Render(truncate(hint, inner/2)), inner+1))
	}
	arrows := func(v string) string { return sMuted.Render("‹ ") + sBold.Render(v) + sMuted.Render(" ›") }

	// model
	model := currentModel(a)
	label := model
	if md := st.modelByID(model); md != nil {
		label = md.Label
	}
	hint := "enter: search all models"
	if st.loading {
		hint = "loading models…"
	} else if st.err != "" {
		hint = st.err
	}
	row(setModel, "Model", arrows(orDefault(label, "default")), hint)

	// thinking
	cur := deref(a.Snap.ThinkingOptionID)
	if cur == "" {
		cur = a.Thinking
	}
	tLabel, tHint := cur, ""
	for _, o := range st.thinkingOptions(a) {
		if o.ID == cur {
			tLabel, tHint = o.Label, o.Description
		}
	}
	if len(st.thinkingOptions(a)) == 0 && !st.loading {
		tHint = "not adjustable for this model"
	}
	row(setThinking, "Thinking", arrows(orDefault(tLabel, "auto")), tHint)

	// mode
	modeID := deref(a.Snap.CurrentModeID)
	mLabel, mHint := modeID, ""
	var ids []string
	for _, md := range a.Snap.AvailableModes {
		ids = append(ids, md.ID)
		if md.ID == modeID {
			mLabel = orDefault(md.Label, md.ID)
			mHint = md.Description
		}
	}
	if mHint == "" {
		mHint = strings.Join(ids, " · ")
	}
	row(setMode, "Mode", arrows(orDefault(mLabel, "default")), mHint)

	// provider toggles (e.g. Fast)
	for i, f := range m.toggles(a) {
		box := sMuted.Render("[ ] off")
		if f.on() {
			box = sGreen.Render("[x] on")
		}
		row(setFeatures+i, f.Label, box, "space to toggle")
	}

	lines = append(lines, "", sMuted.Render("  ↑↓ choose · ←/→ change · enter models · esc close"))
	return frame(lines, w)
}

func frame(lines []string, w int) string {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
		Padding(0, 1).Width(w).Render(strings.Join(lines, "\n"))
}
