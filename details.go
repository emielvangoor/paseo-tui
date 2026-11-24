package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// The agent details panel on the right (d): everything about the focused agent
// that isn't needed at a glance.

func (m *Model) detailsW() int {
	// hide on narrow terminals so the transcript keeps a usable width
	if !m.state.ShowDetails || m.width-m.sidebarW()-m.detWidth() < minMainWidth {
		return 0
	}
	return m.detWidth() + 1
}

// mainW is the width of the centre column.
func (m *Model) mainW() int { return max(20, m.width-m.sidebarW()-m.detailsW()) }

func (m Model) viewDetails(h int) string {
	return m.detailsFrame(m.viewDetailsLines(), m.detWidth(), h)
}

// viewDetailsLines builds every line of the panel (unscrolled).
func (m Model) viewDetailsLines() []string {
	w := m.detWidth()
	inner := w - 2
	var b []string
	section := func(title string) {
		if len(b) > 0 {
			b = append(b, "")
		}
		b = append(b, sMuted.Render(strings.ToUpper(title)))
	}
	kv := func(k, v string) {
		b = append(b, sMuted.Render(fmt.Sprintf("%-9s", k))+truncate(v, inner-9))
	}

	a := m.current()
	if a == nil {
		b = append(b, sMuted.Render("No agent selected."))
		return b
	}
	s := a.Snap

	// agent
	section("Agent")
	for _, l := range strings.Split(lipgloss.NewStyle().Width(inner).Render(agentLabel(*a)), "\n")[:min(3, len(strings.Split(lipgloss.NewStyle().Width(inner).Render(agentLabel(*a)), "\n")))] {
		b = append(b, sBold.Render(l))
	}
	status := sGreen.Render(a.Status)
	switch a.Status {
	case "running":
		status = m.spin.View() + sYellow.Render(" running")
		if t := m.turnStarted(*a); !t.IsZero() {
			status += sYellow.Render(" " + elapsed(t))
		}
	case "error":
		status = sRed.Render("error")
	}
	if s.RequiresAttention && a.Status != "running" {
		status += sBlue.Render("  ◆ unseen")
	}
	kv("status", status)
	provider, model := s.Provider, ""
	if parts := strings.SplitN(a.Provider, "/", 2); len(parts) == 2 {
		model = parts[1]
	}
	kv("provider", provider)
	if model != "" {
		kv("model", model)
	}
	if mode := deref(s.CurrentModeID); mode != "" {
		kv("mode", mode)
	}
	kv("thinking", a.Thinking)
	b = append(b, sMuted.Render("m: change model/thinking/mode"))
	kv("id", a.ShortID)
	kv("created", agoPhrase(a.CreatedAt))
	kv("active", agoPhrase(a.UpdatedAt))
	if e := deref(s.LastError); e != "" {
		kv("error", sRed.Render(firstLine(e)))
	}

	// context & cost
	if u := s.LastUsage; u != nil {
		section("Context & cost")
		if u.ContextWindowMaxTokens > 0 {
			pct := float64(u.ContextWindowUsed) / float64(u.ContextWindowMaxTokens)
			barW := inner - 6
			filled := int(pct * float64(barW))
			col := sGreen
			if pct > 0.8 {
				col = sRed
			} else if pct > 0.6 {
				col = sYellow
			}
			b = append(b, col.Render(strings.Repeat("█", filled))+sMuted.Foreground(cBorder).Render(strings.Repeat("░", max(0, barW-filled)))+
				fmt.Sprintf(" %3d%%", int(pct*100)))
			b = append(b, sMuted.Render(fmt.Sprintf("%s / %s tokens", humanTokens(u.ContextWindowUsed), humanTokens(u.ContextWindowMaxTokens))))
		}
		kv("input", humanTokens(u.InputTokens))
		kv("cached", humanTokens(u.CachedInputTokens))
		kv("output", humanTokens(u.OutputTokens))
		if u.TotalCostUsd > 0 {
			kv("cost", fmt.Sprintf("$%.2f", u.TotalCostUsd))
		}
	}

	// permission
	if perms := m.perms[a.ID]; len(perms) > 0 {
		section("Needs permission")
		for _, p := range perms {
			b = append(b, sRed.Bold(true).Render("! ")+truncate(p.Name+" "+sMuted.Render(permissionSummary(p)), inner-2))
		}
		b = append(b, sGreen.Render("y allow")+sMuted.Render(" · ")+sRed.Render("x deny"))
	}

	// tasks (latest todo list in the transcript)
	if m.selSub == nil {
		for i := len(m.timeline.Entries) - 1; i >= 0; i-- {
			e := m.timeline.Entries[i]
			if e.Kind != "todo" || len(e.Todos) == 0 {
				continue
			}
			done := 0
			for _, t := range e.Todos {
				if t.Completed || t.Status == "completed" {
					done++
				}
			}
			section(fmt.Sprintf("Tasks  %d/%d", done, len(e.Todos)))
			for _, t := range e.Todos {
				mark, st, text := "○", sMuted, t.Text
				switch {
				case t.Completed || t.Status == "completed":
					mark, st = "✓", sGreen
				case t.Status == "in_progress":
					mark, st = "◐", sYellow
					if t.ActiveForm != "" {
						text = t.ActiveForm
					}
				}
				b = append(b, st.Render(mark)+" "+truncate(text, inner-2))
			}
			break
		}
	}

	// subagents
	if subs := m.subagentsOf(a.ID); len(subs) > 0 {
		section(fmt.Sprintf("Subagents  %d", len(subs)))
		sort.SliceStable(subs, func(i, j int) bool {
			bi, bj := subs[i].bucket(), subs[j].bucket()
			if bi != bj {
				return bi > bj // running / failing first
			}
			return subs[i].when().After(subs[j].when())
		})
		for _, e := range subs[:min(6, len(subs))] {
			b = append(b, m.bucketIcon(e.bucket(), "")+" "+truncate(e.label(), inner-2))
		}
		if len(subs) > 6 {
			b = append(b, sMuted.Render(fmt.Sprintf("+%d more · u to open", len(subs)-6)))
		} else {
			b = append(b, sMuted.Render("u to open"))
		}
	}

	// workspace
	if v := m.wsViews[m.selWS]; v != nil {
		ws := v.ws
		section("Workspace")
		kv("name", ws.DisplayName())
		kv("project", ws.ProjectName())
		if br := ws.Branch(); br != "" {
			kv("branch", br)
		}
		kind := ws.WorkspaceKind
		if kind == "" {
			kind = "local"
		}
		kv("kind", strings.ReplaceAll(kind, "_", " "))
		if gh := ws.GithubRuntime; gh != nil && gh.PullRequest != nil {
			pr := gh.PullRequest
			st := pr.State
			if pr.IsMerged {
				st = "merged"
			} else if pr.IsDraft {
				st = "draft"
			}
			kv("PR", fmt.Sprintf("#%d %s", pr.Number, st))
		}
		if d := ws.DiffStat; d != nil && (d.Additions > 0 || d.Deletions > 0) {
			kv("diff", sGreen.Render(fmt.Sprintf("+%d", d.Additions))+" "+sRed.Render(fmt.Sprintf("−%d", d.Deletions)))
		}
		dir := ws.WorkspaceDirectory
		if dir == "" {
			dir = a.Cwd
		}
		b = append(b, sMuted.Render(truncate(shortHome(dir), inner)))
	}

	return b
}

// detailsFrame shows a window of the panel's lines at the current scroll
// offset, with markers when there is more above or below.
func (m Model) detailsFrame(lines []string, w, h int) string {
	// one entry = one row: clip anything wider than the panel instead of letting it wrap
	clipped := make([]string, len(lines))
	for i, l := range lines {
		clipped[i] = truncate(l, w-2)
	}
	lines = clipped
	maxScroll := max(0, len(lines)-h)
	off := min(max(0, m.detailsScroll), maxScroll)
	view := lines
	if len(lines) > h {
		view = append([]string{}, lines[off:min(len(lines), off+h)]...)
		if off > 0 {
			view[0] = sMuted.Render(fmt.Sprintf("↑ %d more · alt+↑", off))
		}
		if below := len(lines) - off - h; below > 0 {
			view[len(view)-1] = sMuted.Render(fmt.Sprintf("↓ %d more · alt+↓", below+1))
		}
	}
	body := lipgloss.NewStyle().Padding(0, 1).Width(w).Render(strings.Join(view, "\n"))
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(body)
}

// scrollDetails moves the details panel; the view clamps it to the content.
func (m *Model) scrollDetails(n int) {
	m.detailsScroll = max(0, m.detailsScroll+n)
	// keep the offset from running far past the end, so scrolling back responds at once
	if lines := m.detailsLineCount(); lines > 0 {
		m.detailsScroll = min(m.detailsScroll, max(0, lines-(m.height-1)))
	}
}

func (m *Model) detailsLineCount() int {
	saved := m.detailsScroll
	m.detailsScroll = 0
	out := m.viewDetailsLines()
	m.detailsScroll = saved
	return len(out)
}

// agoPhrase reads naturally in a sentence: "just now", "5m ago", "on Sep 29".
func agoPhrase(t time.Time) string {
	switch a := ago(t); {
	case a == "":
		return "–"
	case a == "now":
		return "just now"
	case strings.Contains(a, " "):
		return "on " + a
	default:
		return a + " ago"
	}
}
