package main

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The settings panel (c): every setting in its section, changed in place and
// saved to config.toml straight away.

type prefsPanel struct {
	sel     int             // index into the settings
	top     int             // first line shown
	editing bool            // typing a text or number value
	input   textinput.Model // its field
	err     string
}

func (m *Model) openPrefs() (tea.Model, tea.Cmd) {
	m.prefsPanel = &prefsPanel{}
	m.overlay = ovPrefs
	if m.focus == focusInput {
		m.focus, m.slash = focusList, nil
		m.input.Blur()
	}
	return *m, nil
}

// changePref applies fn to a copy of the settings, then saves and applies the result.
func (m *Model) changePref(fn func(c *Config, s setting)) {
	c := *m.prefs
	s := c.settings()[m.prefsPanel.sel]
	fn(&c, s)
	c.clamp()
	m.setConfig(&c)
	m.saveSettings()
}

func (m *Model) prefsKey(k tea.KeyMsg) tea.Cmd {
	p := m.prefsPanel
	all := m.prefs.settings()
	s := all[p.sel]
	p.err = ""
	if p.editing {
		switch k.String() {
		case "esc":
			p.editing = false
			return nil
		case "enter":
			v := strings.TrimSpace(p.input.Value())
			if s.kind == setInt {
				n, err := strconv.Atoi(v)
				if err != nil {
					p.err = "Enter a number"
					return nil
				}
				m.changePref(func(_ *Config, s setting) { *s.i = n })
			} else {
				m.changePref(func(_ *Config, s setting) { *s.s = v })
			}
			p.editing = false
			return nil
		}
		var c tea.Cmd
		p.input, c = p.input.Update(k)
		return c
	}
	step := func(d int) {
		m.changePref(func(_ *Config, s setting) {
			switch s.kind {
			case setBool:
				*s.b = !*s.b
			case setInt:
				*s.i += d
			case setChoice:
				i := 0
				for j, ch := range s.choices {
					if ch[0] == *s.s {
						i = j
					}
				}
				*s.s = s.choices[(i+d+len(s.choices))%len(s.choices)][0]
			}
		})
	}
	switch k.String() {
	case "esc", "q", "c":
		m.overlay, m.prefsPanel = ovNone, nil
	case "up", "k":
		p.sel = nextShown(all, p.sel, -1)
	case "down", "j":
		p.sel = nextShown(all, p.sel, 1)
	case "home", "g":
		p.sel = nextShown(all, -1, 1)
	case "end", "G":
		p.sel = nextShown(all, len(all), -1)
	case "left", "h", "-":
		step(-1)
	case "right", "l", "+", "=":
		step(1)
	case "enter", " ":
		if s.kind == setString || (s.kind == setInt && k.String() == "enter") {
			in := pfInput("", s.value())
			in.CharLimit = 300
			in.CursorEnd()
			p.input, p.editing = in, true
			return p.input.Focus()
		}
		step(1)
	case "r":
		def := defaultConfig().settings()[p.sel]
		m.changePref(func(_ *Config, s setting) {
			switch s.kind {
			case setBool:
				*s.b = *def.b
			case setInt:
				*s.i = *def.i
			default:
				*s.s = *def.s
			}
		})
	}
	return nil
}

func (m Model) prefsView() string {
	p := m.prefsPanel
	w := max(56, min(96, m.width-6))
	inner := w - 4
	all := m.prefs.settings()
	def := defaultConfig().settings()
	var lines []string
	selLine := 0
	section := ""
	for i, s := range all {
		if s.hidden {
			continue
		}
		if s.section != section {
			section = s.section
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, sAccent.Render(sectionTitles[section]))
		}
		sel := i == p.sel
		val := s.value()
		switch {
		case sel && p.editing:
			p.input.Width = max(10, inner/2-2)
			val = p.input.View()
		case s.kind == setBool && *s.b:
			val = sGreen.Render("● on")
		case s.kind == setBool:
			val = sMuted.Render("○ off")
		case s.kind == setString && *s.s == "":
			val = sMuted.Render("automatic")
		case s.kind == setInt || s.kind == setChoice:
			val = "‹ " + val + " ›"
		}
		label := "  " + s.label
		if s.value() != def[i].value() {
			label += sMuted.Render(" •") // changed from the default
		}
		line := rightAlign(label, val, inner)
		if sel {
			selLine = len(lines)
			line = fillBg(sAccent.Render("▌")+rightAlign(" "+sBold.Render(s.label)+strings.TrimPrefix(label, "  "+s.label), val, inner-1), inner, cSelBg)
		}
		lines = append(lines, line)
	}

	// the description always takes two lines, so the panel doesn't jump between settings
	const helpLines = 2
	help := strings.Split(lipgloss.NewStyle().Width(inner-2).Render(all[p.sel].help), "\n")
	if len(help) > helpLines {
		help = help[:helpLines]
		help[helpLines-1] = truncate(strings.TrimRight(help[helpLines-1], " ")+" …", inner-2)
	}
	for len(help) < helpLines {
		help = append(help, "")
	}
	room := max(4, m.height-10-helpLines)
	if selLine < p.top+1 {
		p.top = max(0, selLine-1) // keep the section title in view
	}
	if selLine >= p.top+room {
		p.top = selLine - room + 1
	}
	p.top = min(p.top, max(0, len(lines)-room))
	body := lines[p.top:min(len(lines), p.top+room)]

	title := sAccent.Render(" Settings") + sMuted.Render("  "+shortHome(configPath()))
	out := append([]string{title, ""}, body...)
	out = append(out, sMuted.Foreground(cBorder).Render(strings.Repeat("─", inner)))
	for _, h := range help {
		out = append(out, sMuted.Render(" "+h))
	}
	keys := sMuted.Render(" " + truncate("↑↓ choose · ←→ / enter change · r default · esc close  · • not the default", inner-1))
	switch {
	case p.err != "": // in place of the keys, so the panel keeps its height
		keys = sRed.Render(" " + truncate(p.err, inner-1))
	case p.editing:
		keys = sMuted.Render(" enter save · esc cancel")
	}
	out = append(out, "", keys)
	return frame(out, w)
}

// nextShown is the next setting from i in direction d that isn't hidden (i itself at the ends).
func nextShown(all []setting, i, d int) int {
	for j := i + d; j >= 0 && j < len(all); j += d {
		if !all[j].hidden {
			return j
		}
	}
	return min(max(i, 0), len(all)-1)
}
