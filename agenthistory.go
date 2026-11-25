package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// History (H), like the desktop's: every agent on the host, archived ones
// included, most recently active first, searched by the daemon (title,
// workspace, branch, project). enter opens an agent; an archived one is revived
// first, together with its workspace (an archived worktree is recreated).

type historyList struct {
	search  textinput.Model
	entries []Snapshot
	sel     int
	gen     int // the search the shown entries answer
	loading bool
	err     string
}

type historyMsg struct {
	gen     int
	entries []Snapshot
	err     error
}

type historySearchMsg struct{ gen int }

type reviveDoneMsg struct {
	agentID, name string
	err           error
}

func (m *Model) openHistory() (tea.Model, tea.Cmd) {
	if m.br == nil || !m.connected {
		m.setStatus("Not connected to the daemon", true)
		return *m, nil
	}
	in := textinput.New()
	in.Placeholder = "Search agents, workspaces, branches and projects…"
	in.Prompt = "  "
	in.PromptStyle = sAccent
	in.CharLimit = 200
	m.history = &historyList{search: in, loading: true}
	m.overlay = ovHistory
	if m.focus == focusInput {
		m.focus, m.slash = focusList, nil
		m.input.Blur()
	}
	return *m, tea.Batch(m.history.search.Focus(), m.loadHistory(0, ""))
}

func (m *Model) loadHistory(gen int, search string) tea.Cmd {
	br := m.br
	return func() tea.Msg {
		data, err := br.Call("history", map[string]any{"search": search}, 30*time.Second)
		var entries []Snapshot
		if err == nil {
			err = json.Unmarshal(data, &entries)
		}
		return historyMsg{gen: gen, entries: entries, err: err}
	}
}

func (m *Model) historyKey(k tea.KeyMsg) tea.Cmd {
	h := m.history
	switch k.String() {
	case "esc":
		m.overlay, m.history = ovNone, nil
		return nil
	case "up", "ctrl+p":
		h.sel = max(0, h.sel-1)
		return nil
	case "down", "ctrl+n":
		h.sel = min(max(0, len(h.entries)-1), h.sel+1)
		return nil
	case "enter":
		if h.sel >= len(h.entries) {
			return nil
		}
		e := h.entries[h.sel]
		m.overlay, m.history = ovNone, nil
		return m.openFromHistory(e)
	}
	before := h.search.Value()
	var c tea.Cmd
	h.search, c = h.search.Update(k)
	if h.search.Value() == before {
		return c
	}
	// search after a short pause in typing
	h.gen++
	gen := h.gen
	return tea.Batch(c, tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return historySearchMsg{gen} }))
}

// openFromHistory selects a live agent, or revives an archived one (and an
// archived workspace) and then selects it.
func (m *Model) openFromHistory(e Snapshot) tea.Cmd {
	a := e.toAgent()
	name := agentLabel(a)
	wsArchived := e.WorkspaceID != nil && m.wsViews[*e.WorkspaceID] == nil
	if e.ArchivedAt == nil && !wsArchived {
		return m.selectAgent(e.ID)
	}
	m.setStatus("Restoring "+truncate(name, 40)+"…", false)
	br, wsID := m.br, deref(e.WorkspaceID)
	archived := e.ArchivedAt != nil
	return func() tea.Msg {
		_, err := br.Call("revive", map[string]any{"agentId": e.ID, "workspaceId": wsID, "archived": archived}, 5*time.Minute)
		return reviveDoneMsg{agentID: e.ID, name: name, err: err}
	}
}

func (m Model) historyView() string {
	h := m.history
	w := max(60, min(120, m.width-8))
	inner := w - 4
	h.search.Width = inner - 6
	lines := []string{sAccent.Render(" History") + sMuted.Render("  all agents, archived ones included"), h.search.View(), ""}
	switch {
	case h.err != "":
		lines = append(lines, sRed.Render(" "+truncate(h.err, inner-2)))
	case h.loading && len(h.entries) == 0:
		lines = append(lines, " "+m.spin.View()+sMuted.Render(" loading…"))
	case len(h.entries) == 0:
		lines = append(lines, sMuted.Render(" no agents found"))
	}
	budget := max(4, (m.height-12)/2)
	first := max(0, h.sel-budget+1)
	for i := first; i < len(h.entries) && i < first+budget; i++ {
		e := h.entries[i]
		a := e.toAgent()
		where := []string{}
		if p := e.Project; p != nil {
			where = append(where, p.ProjectName)
			if ws := deref(p.WorkspaceName); ws != "" && ws != p.ProjectName {
				where = append(where, ws)
			}
		} else {
			where = append(where, shortHome(e.Cwd))
		}
		state := m.statusDot(a)
		tag := ""
		switch {
		case e.ArchivedAt != nil:
			state, tag = sMuted.Render("○"), sYellow.Render("archived ")
		case e.WorkspaceID != nil && m.wsViews[*e.WorkspaceID] == nil:
			tag = sYellow.Render("workspace archived ")
		}
		when := sMuted.Render(ago(e.UpdatedAt))
		title := truncate(agentLabel(a), inner-30)
		l1 := rightAlign(" "+state+" "+title, tag+when, inner)
		l2 := "   " + sMuted.Render(truncate(strings.Join(where, " › ")+" · "+strings.SplitN(a.Provider, "/", 2)[0], inner-4))
		if i == h.sel {
			l1 = fillBg(sAccent.Render("▌")+l1[1:], inner, cSelBg)
			l2 = fillBg(l2, inner, cSelBg)
		}
		lines = append(lines, l1, l2)
	}
	foot := "↑↓ choose · enter open (restores archived ones) · esc close"
	if n := len(h.entries); n > 0 {
		foot = fmt.Sprintf("%d of %d  ·  ", h.sel+1, n) + foot
	}
	lines = append(lines, "", sMuted.Render(" "+foot))
	return frame(lines, w)
}
