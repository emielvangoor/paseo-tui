package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// ---------- tree ----------

type rowKind int

const (
	rowSection   rowKind = iota // "Pinned", or a status group header
	rowProject                  // project header (project mode)
	rowWorkspace                // a workspace
	rowEmpty                    // placeholder under a project without workspaces
)

type row struct {
	kind    rowKind
	key     string // project id / workspace id / section title
	label   string
	project *projView
	ws      *wsView
	inPin   bool // workspace shown in the Pinned section
	sb      bucket
}

func (r row) id() string { return fmt.Sprintf("%d:%s", r.kind, r.key) }

func (r row) height() int {
	if r.kind == rowWorkspace {
		return 2
	}
	return 1
}

func (r row) selectable() bool { return r.kind == rowProject || r.kind == rowWorkspace }

// rebuildRows derives the sidebar rows and keeps the cursor on the same item.
func (m *Model) rebuildRows() {
	cur := ""
	if r := m.cursorRow(); r != nil {
		cur = r.id()
	}
	m.buildWorkspaces()
	m.rows = m.rows[:0]

	if m.state.GroupByStatus {
		groups := map[bucket][]*wsView{}
		for _, v := range m.wsViews {
			groups[v.bucket] = append(groups[v.bucket], v)
		}
		for _, b := range statusOrder {
			list := groups[b]
			if len(list) == 0 {
				continue
			}
			sort.SliceStable(list, func(i, j int) bool {
				if !list[i].since.Equal(list[j].since) {
					return list[i].since.After(list[j].since)
				}
				return list[i].ws.ProjectName()+list[i].ws.DisplayName() < list[j].ws.ProjectName()+list[j].ws.DisplayName()
			})
			for i, v := range list {
				v.num = i + 1
			}
			title := bucketTitle[b]
			// status headers fold like projects
			hdr := row{kind: rowProject, key: "status:" + title, label: fmt.Sprintf("%s  %d", title, len(list)), sb: b}
			m.rows = append(m.rows, hdr)
			if m.state.Collapsed[hdr.key] {
				continue
			}
			for _, v := range list {
				m.rows = append(m.rows, row{kind: rowWorkspace, key: v.ws.ID, ws: v})
			}
		}
	} else {
		if len(m.pinned) > 0 {
			m.rows = append(m.rows, row{kind: rowSection, key: "pinned", label: "Pinned"})
			for i, v := range m.pinned {
				v.num = i + 1
				m.rows = append(m.rows, row{kind: rowWorkspace, key: v.ws.ID, ws: v, inPin: true})
			}
		}
		for _, p := range m.projects {
			m.rows = append(m.rows, row{kind: rowProject, key: p.id, label: p.name, project: p})
			for i, v := range p.workspaces {
				v.num = i + 1
			}
			if m.state.Collapsed[p.id] {
				continue
			}
			if len(p.workspaces) == 0 {
				m.rows = append(m.rows, row{kind: rowEmpty, key: p.id, project: p})
			}
			for _, v := range p.workspaces {
				m.rows = append(m.rows, row{kind: rowWorkspace, key: v.ws.ID, ws: v})
			}
		}
	}

	m.cursor = -1
	for i, r := range m.rows {
		if r.id() == cur {
			m.cursor = i
			break
		}
	}
	if m.cursor < 0 && m.selWS != "" {
		m.cursor = m.rowOfWorkspace(m.selWS)
	}
	if m.cursor < 0 {
		for i, r := range m.rows {
			if r.kind == rowWorkspace {
				m.cursor = i
				break
			}
		}
	}
	if m.cursor < 0 && len(m.rows) > 0 {
		m.cursor = 0
	}
}

func (m *Model) rowOfWorkspace(id string) int {
	for i, r := range m.rows {
		if r.kind == rowWorkspace && r.key == id {
			return i
		}
	}
	return -1
}

func (m *Model) cursorRow() *row {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return &m.rows[m.cursor]
	}
	return nil
}

func (m *Model) agentByID(id string) *Agent {
	for i := range m.agents {
		if m.agents[i].ID == id {
			return &m.agents[i]
		}
	}
	return nil
}

// projectKeyOf is the collapse key of the project containing a workspace.
func (m *Model) projectKeyOf(v *wsView) string {
	if m.state.GroupByStatus {
		return "status:" + bucketTitle[v.bucket]
	}
	if v.ws.ProjectID != "" {
		return v.ws.ProjectID
	}
	return v.ws.ProjectName()
}

// ---------- rendering ----------

// sidebarHead is the title and any notice lines above the rows.
func (m Model) sidebarHead() []string {
	title := " Workspaces"
	if m.state.GroupByStatus {
		title = " Workspaces · by status"
	}
	st := sBold
	if m.focus == focusList {
		st = sAccent
	}
	head := []string{st.Render(title), ""}
	if !m.loaded {
		head = append(head, " "+m.spin.View()+sMuted.Render(" loading…"))
	} else if m.down {
		head = append(head, sRed.Render(" Daemon unreachable"))
	} else if len(m.rows) == 0 {
		head = append(head, sMuted.Render(" No workspaces on this host."), sMuted.Render(" press n to start an agent"))
	}
	return head
}

// sidebarStart is the first visible row: the cursor stays in view, and the
// wheel can scroll further via sideScroll.
func (m Model) sidebarStart(avail int) int {
	start, used := 0, 0
	for i := m.cursor; i >= 0 && i < len(m.rows); i-- {
		used += m.rows[i].height()
		if used > avail {
			start = i + 1
			break
		}
	}
	return min(max(start, m.sideScroll), max(0, len(m.rows)-1))
}

func (m Model) viewSidebar(h int) string {
	w := m.sideWidth()
	head := m.sidebarHead()
	avail := h - len(head)
	lines := head
	used := 0
	for i := m.sidebarStart(avail); i < len(m.rows); i++ {
		r := m.rows[i]
		if used+r.height() > avail {
			break
		}
		used += r.height()
		lines = append(lines, m.renderRow(r, i == m.cursor, w)...)
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

// rowAtY maps a screen row inside the sidebar to a sidebar row index.
func (m Model) rowAtY(y int) int {
	h := m.height - 1
	head := m.sidebarHead()
	line := y - len(head)
	if line < 0 {
		return -1
	}
	used := 0
	for i := m.sidebarStart(h - len(head)); i < len(m.rows); i++ {
		rh := m.rows[i].height()
		if line < used+rh {
			return i
		}
		used += rh
	}
	return -1
}

// bucketIcon is the leading status indicator, matching the desktop's meaning.
func (m Model) bucketIcon(b bucket, kind string) string {
	switch b {
	case bNeedsInput:
		return sRed.Bold(true).Render("!")
	case bFailed:
		return sRed.Render("✗")
	case bRunning:
		return m.spin.View()
	case bAttention:
		return sBlue.Render("◆")
	}
	if kind == "worktree" {
		return sMuted.Render("⎇")
	}
	return sMuted.Render("·")
}

func rightAlign(left, right string, w int) string {
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right)-1)
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) renderRow(r row, sel bool, w int) []string {
	plain := lipgloss.NewStyle().Width(w).MaxWidth(w)
	st := struct{ Render func(...string) string }{func(s ...string) string {
		line := lipgloss.NewStyle().MaxWidth(w).Render(strings.Join(s, ""))
		if sel {
			return fillBg(line, w, cSelBg)
		}
		return plain.Render(line)
	}}
	cur := " "
	if sel {
		cur = sAccent.Render("▌")
		if m.focus != focusList {
			cur = sMuted.Render("▌")
		}
	}

	switch r.kind {
	case rowSection:
		return []string{st.Render(cur + sMuted.Render(strings.ToUpper(r.label)))}

	case rowEmpty:
		return []string{st.Render(cur + "   " + sMuted.Italic(true).Render("no active workspaces"))}

	case rowProject:
		collapsed := m.state.Collapsed[r.key]
		arrow := "▾"
		if collapsed {
			arrow = "▸"
		}
		name, right := r.label, ""
		if r.project != nil {
			name = r.project.name
			if collapsed {
				// collapsed projects carry the most urgent workspace state, like the app
				if b := m.projectAggregate(r.project); b != bDone {
					right = m.bucketIcon(b, "") + " "
				}
				right += sMuted.Render(fmt.Sprint(len(r.project.workspaces)))
			}
		} else {
			name = r.label // status section header
		}
		left := cur + sMuted.Render(arrow) + " " + sAccent.Render(truncate(name, w-6-lipgloss.Width(right)))
		return []string{st.Render(rightAlign(left, right, w))}

	case rowWorkspace:
		v := r.ws
		ws := v.ws
		when := sMuted.Render(ago(v.since))
		if v.bucket == bRunning && !v.running.IsZero() {
			when = sYellow.Render(elapsed(v.running))
		} else if v.since.IsZero() {
			when = sMuted.Render(ago(v.activity))
		}
		name := ws.DisplayName()
		nameSt := lipgloss.NewStyle()
		if ws.ID == m.selWS {
			nameSt = nameSt.Bold(true)
		}
		indent := "  "
		if m.state.GroupByStatus || r.inPin {
			indent = " "
		}
		left := cur + indent + m.bucketIcon(v.bucket, ws.WorkspaceKind) + " " +
			nameSt.Render(truncate(v.title(), w-7-len(indent)-lipgloss.Width(when)))
		l1 := rightAlign(left, when, w)

		var meta []string
		if m.state.GroupByStatus || r.inPin {
			meta = append(meta, ws.ProjectName())
		}
		if b := ws.Branch(); b != "" && b != name {
			meta = append(meta, "⎇ "+b)
		}
		if gh := ws.GithubRuntime; gh != nil && gh.PullRequest != nil {
			pr := gh.PullRequest
			s := fmt.Sprintf("#%d", pr.Number)
			switch {
			case pr.IsMerged:
				s += " merged"
			case pr.IsDraft:
				s += " draft"
			}
			meta = append(meta, s)
		}
		if d := ws.DiffStat; d != nil && (d.Additions > 0 || d.Deletions > 0) {
			meta = append(meta, sGreen.Render(fmt.Sprintf("+%d", d.Additions))+" "+sRed.Render(fmt.Sprintf("−%d", d.Deletions)))
		}
		if n := len(v.agents); n != 1 {
			meta = append(meta, fmt.Sprintf("%d agents", n))
		}
		if len(meta) == 0 && len(v.agents) == 1 {
			meta = append(meta, strings.SplitN(v.agents[0].Provider, "/", 2)[0])
		}
		l2 := cur + indent + "  " + sMuted.Render(truncate(strings.Join(meta, " · "), w-5-len(indent)))
		return []string{st.Render(l1), st.Render(l2)}
	}
	return nil
}

// ---------- time helpers ----------

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return t.Format("Jan 2")
}

// ---------- persisted UI state ----------

type uiState struct {
	Collapsed     map[string]bool `json:"collapsed"`
	HideSidebar   bool            `json:"hideSidebar"`
	GroupByStatus bool            `json:"groupByStatus"`
	ShowDetails   bool            `json:"showDetails"`
	SidebarWidth  int             `json:"sidebarWidth,omitempty"`
	DetailsWidth  int             `json:"detailsWidth,omitempty"`
	// remembered New workspace choices, like the desktop
	NewIsolation string `json:"newIsolation,omitempty"` // "local" | "worktree"
	NewProvider  string `json:"newProvider,omitempty"`
	NewModel     string `json:"newModel,omitempty"`
	NewThinking  string `json:"newThinking,omitempty"`
}

type stateFile map[string]*uiState // keyed by host label

func statePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "paseo-tui", "state.json")
}

func loadState(host string) *uiState {
	s := &uiState{Collapsed: map[string]bool{}}
	b, err := os.ReadFile(statePath())
	if err != nil {
		return s
	}
	var f stateFile
	if json.Unmarshal(b, &f) == nil && f[host] != nil {
		s = f[host]
		if s.Collapsed == nil {
			s.Collapsed = map[string]bool{}
		}
	}
	return s
}

func saveState(host string, s *uiState) {
	p := statePath()
	if p == "" {
		return
	}
	f := stateFile{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	f[host] = s
	b, _ := json.MarshalIndent(f, "", "  ")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, b, 0o644)
}

// fillBg paints a background behind an already-styled line. Inner ANSI resets
// would otherwise cancel it, leaving holes behind every coloured segment.
func fillBg(line string, width int, bg lipgloss.TerminalColor) string {
	probe := lipgloss.NewStyle().Background(bg).Render(" ")
	i := strings.Index(probe, " ")
	if i <= 0 {
		return lipgloss.NewStyle().Width(width).Background(bg).Render(line)
	}
	on := probe[:i]
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+on)
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+on)
	pad := max(0, width-lipgloss.Width(line))
	return on + line + strings.Repeat(" ", pad) + "\x1b[0m"
}
