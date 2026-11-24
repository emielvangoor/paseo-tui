package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Mouse text selection in the transcript, copied to the clipboard on release
// (the app captures the mouse, so the terminal's own selection can't be used).

type textPos struct{ line, col int }

type selection struct {
	active   bool // a selection exists (shown highlighted)
	dragging bool
	anchor   textPos
	head     textPos
}

func (s selection) ordered() (textPos, textPos) {
	a, b := s.anchor, s.head
	if b.line < a.line || (b.line == a.line && b.col < a.col) {
		a, b = b, a
	}
	return a, b
}

type copiedMsg struct {
	n   int
	err error
}

// vpOrigin is the screen cell where the transcript viewport starts.
func (m *Model) vpOrigin() (x, y int) {
	return m.sidebarW(), 2 /*workspace line, tab bar*/
}

// posAt maps a screen cell to a transcript position, clamped to the viewport.
func (m *Model) posAt(x, y int) (textPos, bool) {
	ox, oy := m.vpOrigin()
	inside := x >= ox && x < ox+m.vp.Width && y >= oy && y < oy+m.vp.Height
	row := min(max(y-oy, 0), m.vp.Height-1)
	line := m.vp.YOffset + row
	if line >= len(m.vpLines) {
		line = max(0, len(m.vpLines)-1)
	}
	return textPos{line: line, col: max(0, x-ox)}, inside
}

// handleMouse returns (handled, cmd).
func (m *Model) handleMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	switch {
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		p, inside := m.posAt(msg.X, msg.Y)
		if !inside {
			if m.sel.active {
				m.sel = selection{}
			}
			return false, nil
		}
		m.sel = selection{active: true, dragging: true, anchor: p, head: p}
		return true, nil

	case msg.Action == tea.MouseActionMotion && m.sel.dragging:
		_, oy := m.vpOrigin()
		// dragging past the edges scrolls the transcript
		if msg.Y < oy {
			m.vp.LineUp(1)
		} else if msg.Y >= oy+m.vp.Height {
			m.vp.LineDown(1)
		}
		m.follow = m.vp.AtBottom()
		m.sel.head, _ = m.posAt(msg.X, msg.Y)
		return true, nil

	case msg.Action == tea.MouseActionRelease && m.sel.dragging:
		m.sel.dragging = false
		text := m.selectedText()
		if strings.TrimSpace(text) == "" {
			m.sel = selection{} // a plain click just clears
			return true, nil
		}
		return true, copyToClipboard(text)
	}
	return false, nil
}

func (m *Model) selectedText() string {
	a, b := m.sel.ordered()
	var out []string
	for i := a.line; i <= b.line && i < len(m.vpLines); i++ {
		plain := ansi.Strip(m.vpLines[i])
		from, to := 0, ansi.StringWidth(plain)
		if i == a.line {
			from = min(a.col, to)
		}
		if i == b.line {
			to = min(b.col+1, to)
		}
		if from < to {
			out = append(out, strings.TrimRight(ansi.Cut(plain, from, to), " "))
		} else {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

// viewportView renders the transcript, with the selection highlighted.
func (m Model) viewportView() string {
	if !m.sel.active {
		return m.vp.View()
	}
	a, b := m.sel.ordered()
	hl := lipgloss.NewStyle().Reverse(true)
	var rows []string
	for r := 0; r < m.vp.Height; r++ {
		i := m.vp.YOffset + r
		line := ""
		if i < len(m.vpLines) {
			line = m.vpLines[i]
		}
		if i >= a.line && i <= b.line {
			w := max(ansi.StringWidth(line), 1)
			from, to := 0, w
			if i == a.line {
				from = min(a.col, w)
			}
			if i == b.line {
				to = min(b.col+1, w)
			}
			if from < to {
				line = ansi.Truncate(line, from, "") + "\x1b[0m" +
					hl.Render(ansi.Strip(ansi.Cut(line, from, to))) +
					ansi.TruncateLeft(line, to, "")
			}
		}
		rows = append(rows, lipgloss.NewStyle().MaxWidth(m.vp.Width).Render(line))
	}
	return strings.Join(rows, "\n")
}

// copyToClipboard tries wl-copy, then xclip, then OSC 52 (which also works over SSH).
func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		try := func(name string, args ...string) bool {
			if _, err := exec.LookPath(name); err != nil {
				return false
			}
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run() == nil
		}
		if os.Getenv("WAYLAND_DISPLAY") != "" && try("wl-copy") {
			return copiedMsg{n: len([]rune(text))}
		}
		if runtime.GOOS == "darwin" && try("pbcopy") {
			return copiedMsg{n: len([]rune(text))}
		}
		if runtime.GOOS == "windows" && try("powershell", "-NoProfile", "-Command", "$input | Set-Clipboard") {
			return copiedMsg{n: len([]rune(text))}
		}
		if os.Getenv("DISPLAY") != "" && try("xclip", "-selection", "clipboard") {
			return copiedMsg{n: len([]rune(text))}
		}
		// OSC 52: the terminal sets its own clipboard
		seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
		if _, err := os.Stdout.WriteString(seq); err != nil {
			return copiedMsg{err: fmt.Errorf("no clipboard available: %w", err)}
		}
		return copiedMsg{n: len([]rune(text))}
	}
}
