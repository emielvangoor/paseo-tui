package main

import tea "github.com/charmbracelet/bubbletea"

// Resizable side panels: drag the │ dividers with the mouse, or move them with
// < > (left sidebar) and , . (details panel). Widths are saved per host.

const (
	defaultSidebarWidth = 36
	defaultDetailsWidth = 34
	minSidebarWidth     = 22
	minDetailsWidth     = 24
	maxPanelWidth       = 90
	minMainWidth        = 40
	resizeStep          = 4
)

type dragTarget int

const (
	dragNone dragTarget = iota
	dragSidebar
	dragDetails
)

func (m *Model) sideWidth() int {
	w := m.state.SidebarWidth
	if w == 0 {
		w = defaultSidebarWidth
	}
	return w
}

func (m *Model) detWidth() int {
	w := m.state.DetailsWidth
	if w == 0 {
		w = defaultDetailsWidth
	}
	return w
}

// setSidebarWidth clamps so the transcript keeps a usable width.
func (m *Model) setSidebarWidth(w int) {
	room := m.width - m.detailsW() - minMainWidth - 1
	m.state.SidebarWidth = max(minSidebarWidth, min(w, maxPanelWidth, room))
	m.layout()
}

func (m *Model) setDetailsWidth(w int) {
	room := m.width - m.sidebarW() - minMainWidth - 1
	m.state.DetailsWidth = max(minDetailsWidth, min(w, maxPanelWidth, room))
	m.layout()
}

// resizeKey handles < > , . ; returns false when the key isn't a resize key.
func (m *Model) resizeKey(key string) bool {
	switch key {
	case "<":
		if m.state.HideSidebar {
			return false
		}
		m.setSidebarWidth(m.sideWidth() - resizeStep)
	case ">":
		if m.state.HideSidebar {
			return false
		}
		m.setSidebarWidth(m.sideWidth() + resizeStep)
	case ",":
		if m.detailsW() == 0 {
			return false
		}
		m.setDetailsWidth(m.detWidth() + resizeStep) // divider moves left: panel grows
	case ".":
		if m.detailsW() == 0 {
			return false
		}
		m.setDetailsWidth(m.detWidth() - resizeStep)
	default:
		return false
	}
	saveState(m.hostKey, m.state)
	return true
}

// dragDivider starts, continues and ends a divider drag. It returns true when
// the mouse event belonged to a drag.
func (m *Model) dragDivider(msg tea.MouseMsg) bool {
	leftDiv := -1
	if !m.state.HideSidebar {
		leftDiv = m.sideWidth()
	}
	rightDiv := -1
	if m.detailsW() > 0 {
		rightDiv = m.sidebarW() + m.mainW()
	}
	switch {
	case msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && msg.Y < m.bodyH():
		switch {
		case leftDiv >= 0 && abs(msg.X-leftDiv) <= 0:
			m.drag = dragSidebar
		case rightDiv >= 0 && abs(msg.X-rightDiv) <= 0:
			m.drag = dragDetails
		default:
			return false
		}
		return true
	case msg.Action == tea.MouseActionMotion && m.drag != dragNone:
		if m.drag == dragSidebar {
			m.setSidebarWidth(msg.X)
		} else {
			m.setDetailsWidth(m.width - msg.X - 1)
		}
		return true
	case msg.Action == tea.MouseActionRelease && m.drag != dragNone:
		m.drag = dragNone
		saveState(m.hostKey, m.state)
		return true
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
