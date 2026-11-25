package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The archive / remove menu (a): archive the agent, archive the workspace, or
// remove the project from Paseo, depending on what's focused.

type menuItem struct {
	label, target, hint string
	confirm             string // question asked before running
	run                 func() tea.Cmd
}

type ActionMenu struct {
	title string
	items []menuItem
	sel   int
}

func (m *Model) openArchiveMenu() (tea.Model, tea.Cmd) {
	menu := &ActionMenu{title: "Archive, delete or remove"}

	// sidebar cursor on a project header: that project
	var proj *projView
	if r := m.cursorRow(); r != nil && r.kind == rowProject && r.project != nil && m.focus == focusList && !m.state.HideSidebar {
		proj = r.project
	}
	v := m.wsViews[m.selWS]
	if proj == nil && v != nil {
		for _, p := range m.projects {
			for _, w := range p.workspaces {
				if w == v {
					proj = p
				}
			}
		}
		if proj == nil && v.ws.ProjectID != "" {
			for _, p := range m.projects {
				if p.id == v.ws.ProjectID {
					proj = p
				}
			}
		}
	}
	onProjectRow := m.cursorRow() != nil && m.cursorRow().kind == rowProject && m.focus == focusList && !m.state.HideSidebar

	if a := m.current(); a != nil && m.selSub == nil && !onProjectRow {
		id, name := a.ID, agentLabel(*a)
		menu.items = append(menu.items, menuItem{
			label: "Archive agent", target: fmt.Sprintf("%q", truncate(name, 34)), hint: "its workspace stays",
			confirm: fmt.Sprintf("Archive agent %q?", truncate(name, 50)),
			run: func() tea.Cmd {
				return m.call("Archived "+truncate(name, 40), "archive", map[string]any{"agentId": id})
			},
		})
	}
	if v != nil && !v.ws.synthetic && !onProjectRow {
		ws := v.ws
		n := len(v.agents)
		target := fmt.Sprintf("%q", truncate(ws.DisplayName(), 30))
		if n > 0 {
			target += fmt.Sprintf(" and its %d agent%s", n, map[bool]string{true: "", false: "s"}[n == 1])
		}
		hint := "restorable later"
		q := fmt.Sprintf("Archive workspace %q?", ws.DisplayName())
		if ws.IsWorktree() {
			hint = "restorable; worktree keeps its branch"
			q += "\n\nIts worktree is removed from disk; restoring it recreates the worktree on branch " + orDefault(ws.Branch(), "(its branch)") + "."
			if (ws.GitRuntime != nil && ws.GitRuntime.IsDirty) || (ws.DiffStat != nil && (ws.DiffStat.Additions > 0 || ws.DiffStat.Deletions > 0)) {
				q += "\n" + sRed.Render("It has uncommitted changes.")
			}
		}
		wsID, wsName := ws.ID, ws.DisplayName()
		menu.items = append(menu.items, menuItem{
			label: "Archive workspace", target: target, hint: hint, confirm: q,
			run: func() tea.Cmd {
				return m.call("Archived workspace "+truncate(wsName, 40), "archiveWorkspace", map[string]any{"workspaceId": wsID})
			},
		})
	}
	if a := m.current(); a != nil && m.selSub == nil && !onProjectRow {
		c := deleteAgent(*m, *a)
		menu.items = append(menu.items, menuItem{
			label: "Delete agent", target: fmt.Sprintf("%q", truncate(agentLabel(*a), 34)), hint: "permanent: no restore",
			confirm: c.text, run: c.yes,
		})
	}
	if proj != nil && !strings.HasPrefix(proj.id, "cwd:") {
		pid, pname := proj.id, proj.name
		n := len(proj.workspaces)
		menu.items = append(menu.items, menuItem{
			label: "Remove project", target: fmt.Sprintf("%q from Paseo", truncate(pname, 24)),
			hint:    fmt.Sprintf("%d workspace%s · files on disk unchanged", n, map[bool]string{true: "", false: "s"}[n == 1]),
			confirm: fmt.Sprintf("Remove project %q from the sidebar?\n\nIts %d workspace(s) are archived. Files on disk will not be changed.", pname, n),
			run: func() tea.Cmd {
				return m.call("Removed project "+pname, "removeProject", map[string]any{"projectId": pid})
			},
		})
	}
	if len(menu.items) == 0 {
		m.setStatus("Nothing to archive here", true)
		return *m, nil
	}
	m.menu = menu
	m.overlay = ovMenu
	return *m, nil
}

func (m *Model) menuKey(k tea.KeyMsg) tea.Cmd {
	mn := m.menu
	switch k.String() {
	case "esc", "q", "a":
		m.overlay, m.menu = ovNone, nil
	case "up", "k", "shift+tab":
		mn.sel = (mn.sel - 1 + len(mn.items)) % len(mn.items)
	case "down", "j", "tab":
		mn.sel = (mn.sel + 1) % len(mn.items)
	case "enter":
		it := mn.items[mn.sel]
		m.overlay, m.menu = ovConfirm, nil
		m.conf = &confirmation{text: it.confirm, yes: it.run}
	}
	return nil
}

func (m Model) menuView() string {
	mn := m.menu
	w := max(60, min(100, m.width-8))
	inner := w - 4
	lines := []string{sAccent.Render(" " + mn.title), ""}
	for i, it := range mn.items {
		line := fmt.Sprintf("%-18s", it.label) + it.target
		if i == mn.sel {
			line = rightAlign(sAccent.Render("▸ ")+sBold.Render(fmt.Sprintf("%-18s", it.label))+it.target, sMuted.Render(it.hint), inner)
			line = fillBg(line, inner, cSelBg)
		} else {
			line = rightAlign("  "+sMuted.Render(fmt.Sprintf("%-18s", it.label))+it.target, sMuted.Render(it.hint), inner)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", sMuted.Render("  ↑↓ choose · enter continue (asks to confirm) · esc cancel"))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
		Padding(0, 1).Width(w).Render(strings.Join(lines, "\n"))
}
