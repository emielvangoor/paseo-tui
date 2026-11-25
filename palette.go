package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Palette is the floating quick switcher (p / ctrl+p), modelled on the desktop's
// command center: workspaces (searching the agents inside them) and actions.
type Palette struct {
	input   textinput.Model
	ws      []paletteWS
	actions []paletteAction
	rows    []paletteRow
	sel     int
	width   int
	height  int
	total   int
}

type paletteWS struct {
	wsID   string
	title  string
	sub    string
	kind   string // workspace kind; the status icon is looked up live when drawn
	when   time.Time
	hay    string
	agents []paletteAgent
	cur    bool
}

type paletteAgent struct {
	id, title string
}

type paletteAction struct {
	key, title, hint string
}

// paletteRow is one rendered entry (or a section header).
type paletteRow struct {
	header string
	ws     *paletteWS
	agent  *paletteAgent // the agent that matched better than its workspace
	action *paletteAction
	score  int
	pos    []int
	apos   []int
}

// palettePick is what the user chose.
type palettePick struct {
	wsID, agentID, action string
}

func NewPalette(m *Model) *Palette {
	ti := textinput.New()
	ti.Placeholder = "Search workspaces and agents, or type a command…"
	ti.Prompt = "  "
	ti.PromptStyle = sAccent
	ti.CharLimit = 200
	ti.Focus()
	p := &Palette{input: ti}

	for _, v := range m.wsViews {
		w := v.ws
		sub := []string{w.ProjectName()}
		if b := w.Branch(); b != "" && b != w.DisplayName() {
			sub = append(sub, "⎇ "+b)
		}
		if n := len(v.agents); n > 0 {
			sub = append(sub, fmt.Sprintf("%d agent%s", n, map[bool]string{true: "", false: "s"}[n == 1]))
		}
		pw := paletteWS{
			wsID:  w.ID,
			title: w.DisplayName(),
			sub:   strings.Join(sub, " · "),
			kind:  w.WorkspaceKind,
			when:  v.activity,
			cur:   w.ID == m.selWS,
		}
		pw.hay = strings.ToLower(pw.title + " " + w.ProjectName() + " " + w.Branch())
		// most recent agent first, as the desktop attaches them
		agents := append([]Agent{}, v.agents...)
		sort.SliceStable(agents, func(i, j int) bool { return agents[i].UpdatedAt.After(agents[j].UpdatedAt) })
		for _, a := range agents {
			pw.agents = append(pw.agents, paletteAgent{id: a.ID, title: agentLabel(a)})
		}
		p.ws = append(p.ws, pw)
	}
	sort.SliceStable(p.ws, func(i, j int) bool { return p.ws[i].when.After(p.ws[j].when) })
	p.total = len(p.ws)

	sidebar := "Hide sidebar"
	if m.state.HideSidebar {
		sidebar = "Show sidebar"
	}
	group := "Group sidebar by status"
	if m.state.GroupByStatus {
		group = "Group sidebar by project"
	}
	archived := "Show archived agents"
	if m.showAll {
		archived = "Hide archived agents"
	}
	files := ""
	if argv := firstInstalled(filesManagers...); argv != nil {
		files = "Browse the workspace's files in " + argv[0]
	} else {
		_, app := fileManager()
		files = "Show the workspace in " + app
	}
	p.actions = []paletteAction{
		{"n", "New agent in this workspace", "n"},
		{"N", "New workspace…", "N"},
		{"f", "Fork agent into a new tab", "f"},
		{"R", "Rename agent", "R"},
		{"H", "History: open or restore archived agents", "H"},
		{"e", "Open the workspace in your editor", "e"},
		{"F", files, "F"},
		{"b", sidebar, "b"},
		{"o", group, "o"},
		{"m", "Agent settings: model, thinking, mode", "m"},
		{"d", "Toggle agent details panel", "d"},
		{"a", "Archive agent / workspace, or remove project", "a"},
		{"z", "Collapse other projects", "z"},
		{"A", archived, "A"},
		{"t", "Toggle thinking", "t"},
		{"v", "Toggle verbose tool output", "v"},
		{"r", "Reload transcript / reconnect", "r"},
		{"?", "Keyboard shortcuts", "?"},
	}
	if m.down && m.local {
		p.actions = append([]paletteAction{{"S", "Start local daemon", "S"}}, p.actions...)
	}
	p.filter()
	return p
}

func (p *Palette) Init() tea.Cmd { return textinput.Blink }

func (p *Palette) SetSize(w, h int) {
	p.width, p.height = w, h
	p.input.Width = p.boxWidth() - 8
}

func (p *Palette) boxWidth() int { return max(40, min(100, p.width-8)) }

func (p *Palette) maxLines() int { return max(6, min(30, p.height-10)) }

func (p *Palette) selectable(i int) bool { return i >= 0 && i < len(p.rows) && p.rows[i].header == "" }

func (p *Palette) move(dir int) {
	for i := p.sel + dir; i >= 0 && i < len(p.rows); i += dir {
		if p.selectable(i) {
			p.sel = i
			return
		}
	}
}

// Update returns (done, pick, cmd).
func (p *Palette) Update(k tea.KeyMsg) (bool, *palettePick, tea.Cmd) {
	switch k.String() {
	case "esc", "ctrl+c":
		return true, nil, nil
	case "enter":
		if !p.selectable(p.sel) {
			return true, nil, nil
		}
		r := p.rows[p.sel]
		switch {
		case r.action != nil:
			return true, &palettePick{action: r.action.key}, nil
		case r.agent != nil:
			return true, &palettePick{wsID: r.ws.wsID, agentID: r.agent.id}, nil
		default:
			return true, &palettePick{wsID: r.ws.wsID}, nil
		}
	case "down", "ctrl+n", "ctrl+j", "tab":
		p.move(1)
		return false, nil, nil
	case "up", "ctrl+p", "ctrl+k", "shift+tab":
		p.move(-1)
		return false, nil, nil
	}
	before := p.input.Value()
	var c tea.Cmd
	p.input, c = p.input.Update(k)
	if p.input.Value() != before {
		p.filter()
	}
	return false, nil, c
}

func (p *Palette) filter() {
	q := strings.ToLower(strings.TrimSpace(p.input.Value()))
	p.rows = p.rows[:0]

	if q == "" {
		p.rows = append(p.rows, paletteRow{header: "Recent workspaces"})
		n := 0
		for i := range p.ws {
			if p.ws[i].cur {
				continue // the open workspace isn't a destination
			}
			p.rows = append(p.rows, paletteRow{ws: &p.ws[i]})
			if n++; n >= 8 {
				break
			}
		}
		p.rows = append(p.rows, paletteRow{header: "Actions"})
		for i := range p.actions {
			p.rows = append(p.rows, paletteRow{action: &p.actions[i]})
		}
	} else {
		var wsRows []paletteRow
		for i := range p.ws {
			w := &p.ws[i]
			score, ok := fuzzyScore(q, w.hay)
			r := paletteRow{ws: w}
			if ok {
				r.score = score
				_, r.pos = fuzzyPositions(q, strings.ToLower(w.title))
			}
			// an agent that matches better than the workspace itself gets its own line
			for j := range w.agents {
				a := &w.agents[j]
				if as, aok := fuzzyScore(q, strings.ToLower(a.title)); aok && (!ok || as > r.score) {
					r.agent, r.score, ok = a, as, true
					_, r.apos = fuzzyPositions(q, strings.ToLower(a.title))
					r.pos = nil
				}
			}
			if ok {
				wsRows = append(wsRows, r)
			}
		}
		sort.SliceStable(wsRows, func(i, j int) bool {
			if wsRows[i].score != wsRows[j].score {
				return wsRows[i].score > wsRows[j].score
			}
			return wsRows[i].ws.when.After(wsRows[j].ws.when)
		})
		var actRows []paletteRow
		for i := range p.actions {
			a := &p.actions[i]
			if score, ok := fuzzyScore(q, strings.ToLower(a.title)); ok {
				_, pos := fuzzyPositions(q, strings.ToLower(a.title))
				actRows = append(actRows, paletteRow{action: a, score: score, pos: pos})
			}
		}
		sort.SliceStable(actRows, func(i, j int) bool { return actRows[i].score > actRows[j].score })
		if len(wsRows) > 0 {
			p.rows = append(p.rows, paletteRow{header: "Workspaces"})
			p.rows = append(p.rows, wsRows...)
		}
		if len(actRows) > 0 {
			p.rows = append(p.rows, paletteRow{header: "Actions"})
			p.rows = append(p.rows, actRows...)
		}
	}
	p.sel = 0
	if !p.selectable(0) {
		p.move(1)
	}
}

// wsIcon / agentIcon draw the live status (so spinners animate while the panel is open).
func wsIcon(m Model, w *paletteWS) string {
	if v := m.wsViews[w.wsID]; v != nil {
		return m.bucketIcon(v.bucket, v.ws.WorkspaceKind)
	}
	return m.bucketIcon(bDone, w.kind)
}

func agentIcon(m Model, id string) string {
	if a := m.agentByID(id); a != nil {
		return m.bucketIcon(agentBucket(a.Snap), "")
	}
	return sMuted.Render("·")
}

func (p *Palette) rowLines(m Model, r paletteRow, sel bool, inner int) []string {
	base := lipgloss.NewStyle()
	rowSt := struct{ Render func(...string) string }{func(s ...string) string {
		line := strings.Join(s, "")
		if sel {
			return fillBg(line, inner, cSelBg)
		}
		return lipgloss.NewStyle().Width(inner).Render(line)
	}}
	mark := "  "
	if sel {
		mark = sAccent.Render("▌ ")
	}
	switch {
	case r.header != "":
		return []string{sMuted.Render(" " + strings.ToUpper(r.header))}
	case r.action != nil:
		title := highlight(r.action.title, r.pos, base.Bold(sel))
		return []string{rowSt.Render(rightAlign(mark+sMuted.Render("›")+" "+title, sMuted.Render(r.action.hint), inner))}
	}
	w := r.ws
	when := sMuted.Render(ago(w.when))
	title := truncate(w.title, inner-8-lipgloss.Width(when))
	lines := []string{
		rowSt.Render(rightAlign(mark+wsIcon(m, w)+" "+highlight(title, r.pos, base.Bold(sel)), when, inner)),
		rowSt.Render("    " + sMuted.Render(truncate(w.sub, inner-5))),
	}
	if r.agent != nil {
		lines = append(lines, rowSt.Render("    "+sMuted.Render("↳ ")+agentIcon(m, r.agent.id)+" "+highlight(truncate(r.agent.title, inner-10), r.apos, base)))
	}
	return lines
}

func (p *Palette) box(m Model) string {
	w := p.boxWidth()
	inner := w - 4
	lines := []string{p.input.View(), sMuted.Foreground(cBorder).Render(strings.Repeat("─", inner))}

	// render rows, scrolling so the selection stays inside the budget
	budget := p.maxLines()
	var rendered [][]string
	for i, r := range p.rows {
		rendered = append(rendered, p.rowLines(m, r, i == p.sel, inner))
	}
	start := 0
	for {
		used := 0
		for i := start; i <= p.sel && i < len(rendered); i++ {
			used += len(rendered[i])
		}
		if used <= budget || start >= p.sel {
			break
		}
		start++
	}
	used := 0
	for i := start; i < len(rendered); i++ {
		if used+len(rendered[i]) > budget {
			break
		}
		lines = append(lines, rendered[i]...)
		used += len(rendered[i])
	}
	if len(p.rows) == 0 {
		lines = append(lines, "", sMuted.Render("  no matches"), "")
	}
	count := fmt.Sprintf("%d workspaces", p.total)
	lines = append(lines, "", rightAlign(sMuted.Render("  ↑↓ move · enter open · esc close"), sMuted.Render(count), inner+1))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
		Padding(0, 1).Width(w).Render(strings.Join(lines, "\n"))
}

// fuzzyScore matches q as a subsequence of s. Consecutive runs, word starts and
// early matches score higher; every query word must match somewhere.
func fuzzyScore(q, s string) (int, bool) {
	total := 0
	for _, word := range strings.Fields(q) {
		score, pos := fuzzyPositions(word, s)
		if pos == nil {
			return 0, false
		}
		total += score
	}
	return total, true
}

func fuzzyPositions(q, s string) (int, []int) {
	if q == "" {
		return 0, nil
	}
	// exact substring is the strongest signal
	if i := strings.Index(s, q); i >= 0 {
		pos := make([]int, 0, len(q))
		ri := len([]rune(s[:i]))
		for j := range []rune(q) {
			pos = append(pos, ri+j)
		}
		bonus := 0
		if i == 0 || !unicode.IsLetter(rune(s[i-1])) {
			bonus = 30
		}
		return 100 + bonus + len(q)*10 - min(i, 50), pos
	}
	qr, sr := []rune(q), []rune(s)
	var pos []int
	score, qi, prev := 0, 0, -2
	for si := 0; si < len(sr) && qi < len(qr); si++ {
		if sr[si] != qr[qi] {
			continue
		}
		score += 1
		if si == prev+1 {
			score += 5
		}
		if si == 0 || !unicode.IsLetter(sr[si-1]) && !unicode.IsDigit(sr[si-1]) {
			score += 8
		}
		pos = append(pos, si)
		prev = si
		qi++
	}
	if qi < len(qr) {
		return 0, nil
	}
	// letters scattered across a long subtitle are noise, not a match
	if span := pos[len(pos)-1] - pos[0] + 1; span > max(len(qr)*3, len(qr)+6) {
		return 0, nil
	}
	return score, pos
}

func highlight(title string, pos []int, base lipgloss.Style) string {
	if len(pos) == 0 {
		return base.Render(title)
	}
	set := map[int]bool{}
	for _, p := range pos {
		set[p] = true
	}
	hl := base.Foreground(cAccent).Bold(true).Underline(true)
	var b strings.Builder
	for i, r := range []rune(title) {
		if set[i] {
			b.WriteString(hl.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}

// Overlay floats the palette over the top third of the screen.
func (p *Palette) Overlay(m Model, screen string) string {
	box := p.box(m)
	return floatOver(screen, box, max(0, (p.width-lipgloss.Width(strings.SplitN(box, "\n", 2)[0]))/2), max(1, p.height/8))
}

// floatOver draws box on top of screen at column x, row y, keeping the
// screen visible around it (panels float over the UI instead of replacing it).
func floatOver(screen, box string, x, y int) string {
	lines := strings.Split(box, "\n")
	bg := strings.Split(screen, "\n")
	bw := 0
	for _, l := range lines {
		bw = max(bw, lipgloss.Width(l))
	}
	for i, line := range lines {
		row := y + i
		if row >= len(bg) {
			break
		}
		under := bg[row]
		left := ansi.Truncate(under, x, "")
		if w := lipgloss.Width(left); w < x {
			left += strings.Repeat(" ", x-w)
		}
		if w := lipgloss.Width(line); w < bw {
			line += strings.Repeat(" ", bw-w)
		}
		right := ansi.TruncateLeft(under, x+bw, "")
		bg[row] = left + "\x1b[0m" + line + "\x1b[0m" + right
	}
	return strings.Join(bg, "\n")
}
