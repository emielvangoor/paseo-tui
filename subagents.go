package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const parentLabel = "paseo.parent-agent-id"

// ProviderSub is a subagent run inside a provider session (omp/Claude Task, …).
type ProviderSub struct {
	ID             string   `json:"id"`
	ParentAgentID  string   `json:"parentAgentId"`
	ParentSubagent *string  `json:"parentSubagentId"`
	Provider       string   `json:"provider"`
	Title          *string  `json:"title"`
	Description    *string  `json:"description"`
	Subtitle       *string  `json:"subtitle"`
	Status         string   `json:"status"` // running | completed | failed | canceled
	CreatedAt      flexTime `json:"createdAt"`
	UpdatedAt      flexTime `json:"updatedAt"`
}

// label names the row: the provider's short id when it is a name (omp uses
// X49o, X50Restart, …), then the task's first real line. The subagent type
// repeats across a fan-out, so it is only a last resort.
func (s *ProviderSub) label() string {
	task := ""
	for _, l := range strings.Split(deref(s.Description), "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#"))
		if l != "" && !strings.EqualFold(l, "target") {
			task = l
			break
		}
	}
	name := ""
	if len(s.ID) <= 24 && !strings.ContainsAny(s.ID, " -") || len(s.ID) <= 12 {
		name = s.ID
	}
	switch {
	case name != "" && task != "":
		return name + " · " + task
	case task != "":
		return task
	case name != "":
		return name
	case deref(s.Title) != "":
		return deref(s.Title)
	}
	return s.ID
}

func (s *ProviderSub) bucket() bucket {
	switch s.Status {
	case "running":
		return bRunning
	case "failed":
		return bFailed
	}
	return bDone
}

// subRef identifies a provider subagent tab.
type subRef struct{ ParentID, SubID string }

// tabItem is one tab: an agent (root or Paseo child) or a provider subagent.
type tabItem struct {
	agentID string
	sub     *subRef
}

func (t tabItem) key() string {
	if t.sub != nil {
		return "sub:" + t.sub.ParentID + "/" + t.sub.SubID
	}
	return t.agentID
}

// ---------- bridge messages ----------

type subagentListMsg struct {
	parentID string
	subs     []ProviderSub
	err      error
}

type subTimelineMsg struct {
	ref  subRef
	rows []json.RawMessage
	err  error
}

func (m *Model) fetchSubagents(parentID string) tea.Cmd {
	br := m.br
	if br == nil || parentID == "" {
		return nil
	}
	return func() tea.Msg {
		data, err := br.Call("subagents", map[string]any{"agentId": parentID}, 20*time.Second)
		var res struct {
			Subagents []ProviderSub `json:"subagents"`
		}
		if err == nil {
			err = json.Unmarshal(data, &res)
		}
		return subagentListMsg{parentID: parentID, subs: res.Subagents, err: err}
	}
}

func (m *Model) fetchSubTimeline(ref subRef) tea.Cmd {
	br := m.br
	if br == nil {
		return nil
	}
	return func() tea.Msg {
		data, err := br.Call("subagentTimeline", map[string]any{"agentId": ref.ParentID, "subagentId": ref.SubID}, 30*time.Second)
		var res struct {
			Rows []struct {
				Item json.RawMessage `json:"item"`
			} `json:"rows"`
			Error *string `json:"error"`
		}
		var items []json.RawMessage
		if err == nil {
			err = json.Unmarshal(data, &res)
			if err == nil && res.Error != nil && *res.Error != "" {
				err = fmt.Errorf("%s", *res.Error)
			}
			for _, r := range res.Rows {
				items = append(items, r.Item)
			}
		}
		return subTimelineMsg{ref: ref, rows: items, err: err}
	}
}

// applySubUpdate handles agent.provider_subagents.update payloads.
func (m *Model) applySubUpdate(raw json.RawMessage) {
	var p struct {
		Kind          string          `json:"kind"`
		Subagent      *ProviderSub    `json:"subagent"`
		ParentAgentID string          `json:"parentAgentId"`
		SubagentID    string          `json:"subagentId"`
		Item          json.RawMessage `json:"item"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	switch p.Kind {
	case "upsert":
		if s := p.Subagent; s != nil {
			if m.subs[s.ParentAgentID] == nil {
				m.subs[s.ParentAgentID] = map[string]*ProviderSub{}
			}
			m.subs[s.ParentAgentID][s.ID] = s
		}
	case "remove":
		delete(m.subs[p.ParentAgentID], p.SubagentID)
	case "timeline":
		if r := m.selSub; r != nil && r.ParentID == p.ParentAgentID && r.SubID == p.SubagentID {
			m.timeline.Apply(p.Item)
			m.refreshViewport()
		}
	}
}

// ---------- what belongs to an agent ----------

func childOf(a Agent) string { return a.Snap.Labels[parentLabel] }

// subagentEntry is one row of an agent's subagent track/list.
type subagentEntry struct {
	agent *Agent       // Paseo child agent
	sub   *ProviderSub // provider subagent
	depth int
}

func (e subagentEntry) bucket() bucket {
	if e.agent != nil {
		return agentBucket(e.agent.Snap)
	}
	return e.sub.bucket()
}

func (e subagentEntry) label() string {
	if e.agent != nil {
		return agentLabel(*e.agent)
	}
	return e.sub.label()
}

func (e subagentEntry) when() time.Time {
	if e.agent != nil {
		return e.agent.UpdatedAt
	}
	return e.sub.UpdatedAt.Time
}

func (e subagentEntry) created() time.Time {
	if e.agent != nil {
		return e.agent.CreatedAt
	}
	return e.sub.CreatedAt.Time
}

func (e subagentEntry) tab() tabItem {
	if e.agent != nil {
		return tabItem{agentID: e.agent.ID}
	}
	return tabItem{sub: &subRef{ParentID: e.sub.ParentAgentID, SubID: e.sub.ID}}
}

// subagentsOf lists an agent's Paseo children and provider subagents, newest
// first, nested subagents indented under the subagent that spawned them.
func (m *Model) subagentsOf(parentID string) []subagentEntry {
	var roots []subagentEntry
	for i := range m.agents {
		if childOf(m.agents[i]) == parentID && m.agents[i].Snap.ArchivedAt == nil {
			roots = append(roots, subagentEntry{agent: &m.agents[i]})
		}
	}
	byParent := map[string][]*ProviderSub{}
	for _, s := range m.subs[parentID] {
		p := deref(s.ParentSubagent)
		if p != "" && m.subs[parentID][p] == nil {
			p = "" // its parent subagent is unknown: list it at the top level
		}
		byParent[p] = append(byParent[p], s)
	}
	for _, list := range byParent {
		sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt.Time) })
	}
	for _, s := range byParent[""] {
		roots = append(roots, subagentEntry{sub: s})
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].created().After(roots[j].created()) })

	var out []subagentEntry
	seen := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, s := range byParent[parent] {
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			out = append(out, subagentEntry{sub: s, depth: depth})
			walk(s.ID, depth+1)
		}
	}
	for _, e := range roots {
		if e.agent != nil {
			out = append(out, e)
			continue
		}
		if !seen[e.sub.ID] {
			seen[e.sub.ID] = true
			out = append(out, e)
			walk(e.sub.ID, 1)
		}
	}
	for _, list := range byParent { // a parent loop never reached from the top level
		for _, s := range list {
			if !seen[s.ID] {
				seen[s.ID] = true
				out = append(out, subagentEntry{sub: s})
			}
		}
	}
	return out
}

// trackLine summarises the focused agent's subagents above the composer, like
// the desktop's pill: active states with counts, or just the total when all done.
func (m Model) trackLine(entries []subagentEntry, w int) string {
	if len(entries) == 0 {
		return ""
	}
	counts := map[bucket]int{}
	for _, e := range entries {
		counts[e.bucket()]++
	}
	var segs []string
	for _, b := range []bucket{bNeedsInput, bFailed, bRunning, bAttention} {
		if n := counts[b]; n > 0 {
			segs = append(segs, m.bucketIcon(b, "")+" "+fmt.Sprintf("%d %s", n, strings.ToLower(bucketTitle[b])))
		}
	}
	total := fmt.Sprintf("%d subagent%s", len(entries), map[bool]string{true: "", false: "s"}[len(entries) == 1])
	line := " " + sAccent.Render("⧉") + " "
	if len(segs) > 0 {
		line += strings.Join(segs, sMuted.Render(" · ")) + sMuted.Render(" · "+total)
	} else {
		line += sMuted.Render(total)
	}
	line += sMuted.Render("  ·  u to open")
	return truncate(line, w)
}

// ---------- floating list (u) ----------

type SubList struct {
	parent  string
	entries []subagentEntry
	sel     int
	width   int
	height  int
}

func (l *SubList) Update(k tea.KeyMsg) (done bool, pick *tabItem) {
	switch k.String() {
	case "esc", "q", "u":
		return true, nil
	case "enter":
		if l.sel < len(l.entries) {
			t := l.entries[l.sel].tab()
			return true, &t
		}
		return true, nil
	case "down", "j", "ctrl+n", "tab":
		if len(l.entries) > 0 {
			l.sel = (l.sel + 1) % len(l.entries)
		}
	case "up", "k", "ctrl+p", "shift+tab":
		if len(l.entries) > 0 {
			l.sel = (l.sel - 1 + len(l.entries)) % len(l.entries)
		}
	case "G", "end":
		l.sel = max(0, len(l.entries)-1)
	case "g", "home":
		l.sel = 0
	}
	return false, nil
}

func (l *SubList) View(m Model) string {
	w := max(40, min(110, l.width-8))
	inner := w - 4
	lines := []string{sAccent.Render(" Subagents") + sMuted.Render(" of "+l.parent), sMuted.Foreground(cBorder).Render(strings.Repeat("─", inner))}
	budget := max(4, (l.height-10)/2)
	start := 0
	if l.sel >= budget {
		start = l.sel - budget + 1
	}
	for i := start; i < len(l.entries) && i < start+budget; i++ {
		e := l.entries[i]
		sel := i == l.sel
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
		indent := strings.Repeat("  ", e.depth)
		kind, sub := "", ""
		if e.agent != nil {
			kind = "agent"
			sub = strings.SplitN(e.agent.Provider, "/", 2)[0] + " · " + e.agent.Status
		} else {
			kind = deref(e.sub.Title)
			sub = firstLine(deref(e.sub.Subtitle))
			if sub == "" {
				sub = e.sub.Status
			}
		}
		when := sMuted.Render(ago(e.when()))
		title := truncate(e.label(), inner-10-len(indent)-lipgloss.Width(when))
		lines = append(lines,
			rowSt.Render(rightAlign(mark+indent+m.bucketIcon(e.bucket(), "")+" "+title, when, inner)),
			rowSt.Render("    "+indent+sMuted.Render(truncate(strings.TrimSpace(kind+" · "+sub), inner-6-len(indent)))))
	}
	if len(l.entries) == 0 {
		lines = append(lines, "", sMuted.Render("  no subagents"), "")
	}
	lines = append(lines, "", rightAlign(sMuted.Render("  ↑↓ move · enter open as tab · esc close"),
		sMuted.Render(fmt.Sprintf("%d of %d", min(l.sel+1, len(l.entries)), len(l.entries))), inner+1))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
		Padding(0, 1).Width(w).Render(strings.Join(lines, "\n"))
}
