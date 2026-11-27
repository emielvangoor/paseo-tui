package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The keyboard help (?): shortcuts in sections, laid out in two columns when
// the window is wide enough, scrollable when it still doesn't fit.

type helpSection struct {
	title string
	rows  [][2]string
}

var helpSections = []helpSection{
	{"Navigate", [][2]string{
		{"p · ctrl+p · :", "command center: workspaces, agents, actions"},
		{"j / k · ↑ / ↓", "move through projects and workspaces"},
		{"home / end", "first / last row of the sidebar"},
		{"{ / }", "previous / next workspace (also with the sidebar hidden)"},
		{"h / l · ← / →", "collapse / expand a project"},
		{"space · enter", "fold / unfold the project under the cursor"},
		{"z", "collapse all other projects"},
		{"o", "group the sidebar by project / by status"},
		{"H", "history: all agents, archived ones too; enter restores"},
		{"U", "plan usage: limits and credits per account; r refreshes"},
	}},
	{"Tabs", [][2]string{
		{"[ / ] · tab", "previous / next agent tab (shift+tab back)"},
		{"1 … 9", "jump to tab N (alt+1 … 9 while writing)"},
		{"u", "subagents; open one as a ↳ tab"},
		{"w · ctrl+w", "close a subagent tab (or click its ×)"},
	}},
	{"Write", [][2]string{
		{"i", "write to the agent (alt+enter adds a line)"},
		{"enter", "same, or review what the agent waits on; sends"},
		{"/", "commands and skills of the agent"},
		{"/clear · /exit", "fresh agent with the same setup · archive it"},
		{"↑ / ↓", "previous / next prompt (in the message box)"},
		{"tab · ⇧tab", "next / previous tab while writing"},
		{"ctrl+v", "paste an image (or paste / drop its path)"},
		{"backspace", "remove the last image (empty message box)"},
		{"esc", "leave the message box"},
	}},
	{"Agents", [][2]string{
		{"n · ctrl+n", "new agent in this workspace"},
		{"N", "new workspace: project, worktree, branch, model"},
		{"f", "fork into a new tab, with the chat history"},
		{"r", "rename the agent (its tab)"},
		{"m", "settings: model, thinking, mode, Fast"},
		{"s", "stop the running agent (asks; s s stops)"},
		{"enter · x", "review (y allow · n deny) · deny a permission"},
		{"enter · x", "answer · dismiss a question"},
		{"a", "archive or delete agent / workspace, remove project"},
		{"delete", "delete the agent (asks; fn+delete on a Mac)"},
		{"A", "show / hide archived agents"},
	}},
	{"Files", [][2]string{
		{"e", "the workspace in your editor, in a pane"},
		{"F", "the workspace in a file manager, in a pane"},
		{"ctrl+\\", "hide the pane (e / F bring it back)"},
	}},
	{"Transcript", [][2]string{
		{"J / K", "scroll"},
		{"ctrl+d / u", "scroll half a page"},
		{"pgdn / pgup", "scroll a page"},
		{"g / G", "top / bottom (G follows new output)"},
		{"t · v", "show thinking · verbose tool output"},
		{"esc", "clear a text selection"},
	}},
	{"Mouse", [][2]string{
		{"click", "open a workspace or tab, fold a project"},
		{"mid-click · ×", "close a subagent tab"},
		{"drag a │", "resize the sidebar or the details panel"},
		{"drag", "select transcript text (copied on release)"},
		{"wheel", "scroll (it accelerates)"},
	}},
	{"Layout", [][2]string{
		{"b · d", "hide / show the sidebar (ctrl+b) · details panel"},
		{"< > · , .", "resize the sidebar · details (or drag)"},
		{"alt+j / k", "scroll the details panel (alt+↓ / ↑)"},
	}},
	{"App", [][2]string{
		{"? · q", "this help · quit (asks; q q or ctrl+c quits)"},
		{"c", "settings (saved to config.toml; edits there apply live)"},
		{"ctrl+r", "reload / reconnect"},
		{"S", "start the local daemon (when it's down)"},
	}},
}

const (
	helpKeyW = 15 // key column
	helpGap  = 4  // between the two columns
)

// helpLines renders the sections in one or two columns for a box of inner width w.
func helpLines(w int) []string {
	render := func(s helpSection, colW int) []string {
		descW := colW - helpKeyW
		out := []string{sAccent.Render(s.title)}
		for _, r := range s.rows {
			key := sBold.Render(fmt.Sprintf("%-*s", helpKeyW, r[0]))
			desc := strings.Split(lipgloss.NewStyle().Width(descW).Render(r[1]), "\n")
			for i, d := range desc {
				d = sMuted.Render(strings.TrimRight(d, " "))
				if i == 0 {
					out = append(out, key+d)
				} else {
					out = append(out, strings.Repeat(" ", helpKeyW)+d)
				}
			}
		}
		return append(out, "")
	}
	colW := (w - helpGap) / 2
	if colW-helpKeyW < 30 { // too narrow for two columns
		var out []string
		for _, s := range helpSections {
			out = append(out, render(s, min(w, helpKeyW+60))...)
		}
		return out
	}
	// sections stay in order; split them where the taller column is shortest
	var blocks [][]string
	total := 0
	for _, s := range helpSections {
		b := render(s, colW)
		blocks = append(blocks, b)
		total += len(b)
	}
	split, best, run := 0, total, 0
	for i, b := range blocks {
		run += len(b)
		if h := max(run, total-run); h < best {
			split, best = i+1, h
		}
	}
	var left, right []string
	for i, b := range blocks {
		if i < split {
			left = append(left, b...)
		} else {
			right = append(right, b...)
		}
	}
	out := make([]string, max(len(left), len(right)))
	for i := range out {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = l + strings.Repeat(" ", max(0, colW+helpGap-lipgloss.Width(l))) + r
	}
	return out
}

func (m Model) helpInner() int { return min(m.width-6, 2*(helpKeyW+52)+helpGap) } // border and padding take 4, keep 2 free

func (m Model) helpRoom() int { return max(3, m.height-6) } // body rows: border, title, footer and a free line around

// helpBody is everything below the title: the sections and the legend.
func (m Model) helpBody() []string {
	legend := sMuted.Render("legend ") + sRed.Bold(true).Render("!") + sMuted.Render(" needs input  ") + sRed.Render("✗") +
		sMuted.Render(" failed  ") + sYellow.Render("⠋") + sMuted.Render(" working  ") + sBlue.Render("◆") +
		sMuted.Render(" ready to review  · ⎇ done")
	return append(helpLines(m.helpInner()), legend)
}

// helpBox is the help panel, scrolled to m.helpScroll when it's taller than the window.
func (m Model) helpBox() string {
	body := m.helpBody()
	room, total := m.helpRoom(), len(body)
	foot := "esc close"
	if total > room {
		scroll := min(max(0, m.helpScroll), total-room)
		body = body[scroll : scroll+room]
		foot = fmt.Sprintf("j/k ↑↓ scroll · space/pgdn page · esc close   %d%%", (scroll+room)*100/total)
	}
	head := sAccent.Render("paseo-tui") + sMuted.Render("  keyboard shortcuts")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).Padding(0, 1).
		Render(head + "\n" + strings.Join(body, "\n") + "\n" + sMuted.Render(foot))
}

// helpKey scrolls or closes the help.
func (m *Model) helpKey(k tea.KeyMsg) {
	switch k.String() {
	case "j", "down":
		m.helpScroll++
	case "k", "up":
		m.helpScroll--
	case " ", "pgdown", "ctrl+d":
		m.helpScroll += max(1, m.height-8)
	case "pgup", "ctrl+u":
		m.helpScroll -= max(1, m.height-8)
	case "g", "home":
		m.helpScroll = 0
	case "G", "end":
		m.helpScroll = 1 << 20
	default: // esc, q, ? and anything else close it
		m.overlay = ovNone
	}
	m.helpScroll = min(max(0, m.helpScroll), m.helpMaxScroll())
}

func (m Model) helpMaxScroll() int { return max(0, len(m.helpBody())-m.helpRoom()) }
