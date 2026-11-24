package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Rename (R): set the focused agent's title, which is also its tab label.

type renameForm struct {
	agentID string
	input   textinput.Model
	err     string
}

func (m Model) openRename() (tea.Model, tea.Cmd) {
	a := m.current()
	if a == nil || m.selSub != nil {
		m.setStatus("Open an agent tab to rename it", true)
		return m, nil
	}
	in := pfInput("agent name", a.Name)
	in.CharLimit = 200
	in.CursorEnd()
	m.renaming = &renameForm{agentID: a.ID, input: in}
	m.overlay = ovRename
	if m.focus == focusInput {
		m.focus = focusList
		m.input.Blur()
	}
	return m, m.renaming.input.Focus()
}

func (m *Model) renameKey(k tea.KeyMsg) tea.Cmd {
	f := m.renaming
	switch k.String() {
	case "esc":
		m.overlay, m.renaming = ovNone, nil
		return nil
	case "enter":
		name := strings.TrimSpace(f.input.Value())
		if name == "" {
			f.err = "Enter a name."
			return nil
		}
		m.overlay, m.renaming = ovNone, nil
		return m.call("Renamed to "+truncate(name, 40), "rename", map[string]any{"agentId": f.agentID, "name": name})
	}
	var c tea.Cmd
	f.input, c = f.input.Update(k)
	f.err = ""
	return c
}

func (m Model) renameView() string {
	f := m.renaming
	w := max(50, min(80, m.width-8))
	f.input.Width = w - 8
	lines := []string{sAccent.Render(" Rename agent"), "", " " + f.input.View()}
	if f.err != "" {
		lines = append(lines, sRed.Render(" "+f.err))
	}
	lines = append(lines, "", sMuted.Render(" enter save · esc cancel"))
	return frame(lines, w)
}
