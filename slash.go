package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Slash commands in the composer, like the desktop: a "/" at the start of the
// message lists the agent's commands and skills, filtered as you type; a "/"
// later in the message (after a space) lists only skills. ↑↓ choose, tab or
// enter inserts the choice, esc closes the list.

type slashCommand struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	ArgumentHint string   `json:"argumentHint"`
	Kind         string   `json:"kind"` // command | skill, or client for paseo-tui's own
	Aliases      []string `json:"-"`
}

// clientCommands are run by paseo-tui itself, never sent to the agent: the
// desktop's own commands, with the same names and aliases.
var clientCommands = []slashCommand{
	{Name: "clear", Aliases: []string{"new"}, Kind: "client",
		Description: "Archive this agent and start a fresh one with the same setup"},
	{Name: "exit", Aliases: []string{"quit", "q"}, Kind: "client", Description: "Archive this agent"},
}

// clientCommand is the client command a whole message names ("/clear", "/q"), if any.
func clientCommand(text string) *slashCommand {
	name, ok := strings.CutPrefix(strings.TrimSpace(text), "/")
	if !ok || name == "" || strings.ContainsAny(name, " \t\n") {
		return nil
	}
	for i, c := range clientCommands {
		if c.Name == name || slices.Contains(c.Aliases, name) {
			return &clientCommands[i]
		}
	}
	return nil
}

// withClientCommands is the agent's list plus the client commands, which win
// over an agent command of the same name (as in the desktop) and come last.
func withClientCommands(cmds []slashCommand) []slashCommand {
	out := make([]slashCommand, 0, len(cmds)+len(clientCommands))
	for _, c := range cmds {
		if clientCommand("/"+c.Name) == nil {
			out = append(out, c)
		}
	}
	return append(out, clientCommands...)
}

type slashMenu struct {
	key    string // agent, line and column of the "/", to remember a dismissed list
	start  int    // rune column of the "/" in the cursor's line
	col    int    // rune column of the cursor
	eol    bool   // the cursor is at the end of its line
	inline bool   // not at the start of the message: skills only
	query  string
	items  []slashCommand
	sel    int
}

type commandsMsg struct {
	agentID string
	cmds    []slashCommand
	err     error
}

func (m *Model) loadCommands(agentID string) tea.Cmd {
	br := m.br
	if br == nil {
		return nil
	}
	m.cmdLoading[agentID] = true
	return func() tea.Msg {
		data, err := br.Call("commands", map[string]any{"agentId": agentID}, 30*time.Second)
		var cmds []slashCommand
		if err == nil {
			err = json.Unmarshal(data, &cmds)
		}
		return commandsMsg{agentID: agentID, cmds: cmds, err: err}
	}
}

// activeSlash finds the "/command" being typed before the cursor: a "/" at the
// start of the line or after whitespace, followed by no spaces, quotes or slashes.
func activeSlash(line []rune, col int) (start int, query string, ok bool) {
	col = min(max(col, 0), len(line))
	for i := col - 1; i >= 0; i-- {
		switch r := line[i]; {
		case r == '/':
			if i > 0 && !unicode.IsSpace(line[i-1]) {
				return 0, "", false
			}
			return i, string(line[i+1 : col]), true
		case unicode.IsSpace(r) || r == '"' || r == '\'':
			return 0, "", false
		}
	}
	return 0, "", false
}

// rankCommands filters cmds by query, best match first: exact, prefix, word
// start, substring, then letters in order; ties by name. An empty query keeps
// the agent's own order. inline keeps only skills.
func rankCommands(cmds []slashCommand, query string, inline bool) []slashCommand {
	q := strings.ToLower(strings.TrimSpace(query))
	type scored struct {
		c           slashCommand
		tier, where int
	}
	var out []scored
	for _, c := range cmds {
		if inline && c.Kind != "skill" {
			continue
		}
		s := scored{c: c, tier: -1}
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			tier, where, ok := matchName(strings.ToLower(name), q)
			if ok && (s.tier < 0 || tier < s.tier || tier == s.tier && where < s.where) {
				s.tier, s.where = tier, where
			}
		}
		if s.tier < 0 {
			continue
		}
		out = append(out, s)
	}
	if q != "" {
		sort.SliceStable(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if a.tier != b.tier {
				return a.tier < b.tier
			}
			if a.where != b.where {
				return a.where < b.where
			}
			return a.c.Name < b.c.Name
		})
	}
	res := make([]slashCommand, len(out))
	for i, s := range out {
		res[i] = s.c
	}
	return res
}

// matchName ranks how q matches name: exact, prefix, word start, substring,
// then letters in order (lower is better), and where.
func matchName(name, q string) (tier, where int, ok bool) {
	switch i := strings.Index(name, q); {
	case q == "" || name == q:
		return 0, 0, true
	case i == 0:
		return 1, 0, true
	case i > 0 && !unicode.IsLetter(rune(name[i-1])) && !unicode.IsDigit(rune(name[i-1])):
		return 2, i, true
	case i > 0:
		return 3, i, true
	}
	at, ok := subsequence(name, q)
	return 4, at, ok
}

// subsequence reports whether q's letters appear in s in order, and where the first one is.
func subsequence(s, q string) (int, bool) {
	first, j := -1, 0
	qr := []rune(q)
	for i, r := range []rune(s) {
		if j < len(qr) && r == qr[j] {
			if first < 0 {
				first = i
			}
			j++
		}
	}
	return first, j == len(qr)
}

// refreshSlash opens, updates or closes the list from the composer's text and
// cursor, loading the agent's commands the first time it opens.
func (m *Model) refreshSlash() tea.Cmd {
	a := m.current()
	if a == nil || m.focus != focusInput || m.selSub != nil {
		m.slash = nil
		return nil
	}
	lines := strings.Split(m.input.Value(), "\n")
	row := m.input.Line()
	if row >= len(lines) {
		m.slash = nil
		return nil
	}
	line := []rune(lines[row])
	li := m.input.LineInfo()
	col := min(li.StartColumn+li.ColumnOffset, len(line))
	start, query, ok := activeSlash(line, col)
	if !ok {
		m.slash, m.slashDismissed = nil, ""
		return nil
	}
	key := fmt.Sprintf("%s:%d:%d", a.ID, row, start)
	if key == m.slashDismissed {
		m.slash = nil
		return nil
	}
	opening := m.slash == nil || m.slash.key != key
	menu := &slashMenu{key: key, start: start, col: col, eol: col == len(line), inline: row != 0 || start != 0, query: query}
	menu.items = rankCommands(withClientCommands(m.commands[a.ID]), query, menu.inline)
	if prev := m.slash; prev != nil && !opening && prev.sel < len(prev.items) {
		for i, c := range menu.items { // keep the highlighted entry while typing
			if c.Name == prev.items[prev.sel].Name {
				menu.sel = i
			}
		}
	}
	m.slash = menu
	if _, cached := m.commands[a.ID]; opening && !cached && !m.cmdLoading[a.ID] {
		return m.loadCommands(a.ID)
	}
	return nil
}

// slashKey handles a key while the list is open; it reports whether it used it.
func (m *Model) slashKey(k tea.KeyMsg) (bool, tea.Cmd) {
	s := m.slash
	switch k.String() {
	case "esc":
		m.slash, m.slashDismissed = nil, s.key
		return true, nil
	case "up", "down":
		if n := len(s.items); n > 0 {
			d := map[string]int{"up": -1, "down": 1}[k.String()]
			s.sel = (s.sel + d + n) % n
		}
		return true, nil
	case "tab", "enter":
		if len(s.items) == 0 {
			return k.String() == "tab", nil // enter with nothing to pick sends the message
		}
		c := s.items[s.sel]
		if c.Kind == "client" && !s.inline && len(m.attachments) == 0 {
			return true, m.runClientCommand(c) // runs at once, like the desktop
		}
		m.acceptSlash(c)
		return true, nil
	}
	return false, nil
}

// acceptSlash replaces the typed "/query" with the chosen "/name".
func (m *Model) acceptSlash(c slashCommand) {
	s := m.slash
	for range s.col - s.start {
		m.input, _ = m.input.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	text := "/" + c.Name
	if s.eol {
		text += " "
	}
	m.input.InsertString(text)
	m.slash, m.slashDismissed = nil, s.key
	m.fitInput()
}

// runClientCommand runs /exit (archive the agent) or /clear (archive it and
// open a fresh agent with its provider, model, thinking, mode and options).
func (m *Model) runClientCommand(c slashCommand) tea.Cmd {
	a := m.current()
	if a == nil || m.selSub != nil {
		return nil
	}
	m.input.Reset()
	m.fitInput()
	m.slash, m.slashDismissed = nil, ""
	archive := m.call("Archived "+truncate(agentLabel(*a), 40), "archive", map[string]any{"agentId": a.ID})
	// leave the message box first: closing the form must not drop keys into another agent's composer
	m.focus = focusList
	m.input.Blur()
	if c.Name != "clear" {
		return archive
	}
	mm, open := m.openFork(true)
	*m = mm.(Model)
	return tea.Batch(archive, open)
}

// slashView is the list, drawn over the bottom of the transcript above the composer.
func (m Model) slashView(w int) string {
	s := m.slash
	inner := max(30, min(w-4, 100))
	title := "Commands and skills"
	if s.inline {
		title = "Skills"
	}
	a := m.current()
	lines := []string{sAccent.Render(" " + title)}
	const budget = 8
	switch {
	case a != nil && m.cmdLoading[a.ID] && len(s.items) == 0:
		lines = append(lines, " "+m.spin.View()+sMuted.Render(" loading…"))
	case a != nil && m.cmdErr[a.ID] != "" && len(s.items) == 0:
		lines = append(lines, sRed.Render(" "+truncate(m.cmdErr[a.ID], inner-2)))
	case len(s.items) == 0:
		lines = append(lines, sMuted.Render(" no matches"))
	}
	first := max(0, s.sel-budget+1)
	for i := first; i < len(s.items) && i < first+budget; i++ {
		c := s.items[i]
		name := "/" + c.Name
		hint := ""
		if c.ArgumentHint != "" {
			hint = " " + c.ArgumentHint
		}
		kind := ""
		switch {
		case c.Kind == "skill" && !s.inline:
			kind = "skill "
		case c.Kind == "client":
			kind = "paseo "
		}
		left := " " + sBold.Render(name) + sMuted.Render(hint)
		right := sMuted.Render(kind)
		desc := truncate(firstLine(c.Description), max(0, inner-lipgloss.Width(left)-lipgloss.Width(right)-3))
		row := rightAlign(left+"  "+sMuted.Render(desc), right, inner)
		if i == s.sel {
			row = fillBg(rightAlign(sAccent.Render("▌")+sBold.Render(name)+sMuted.Render(hint)+"  "+desc, kind, inner), inner, cSelBg)
		}
		lines = append(lines, row)
	}
	if n := len(s.items); n > budget {
		lines = append(lines, sMuted.Render(fmt.Sprintf(" %d of %d", s.sel+1, n)))
	}
	lines = append(lines, sMuted.Render(" ↑↓ choose · tab/enter insert · esc close"))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
		Render(strings.Join(lines, "\n"))
}
