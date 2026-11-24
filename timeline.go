package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type rawItem struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	MessageID string          `json:"messageId"`
	Message   string          `json:"message"`
	Level     string          `json:"level"`
	CallID    string          `json:"callId"`
	Name      string          `json:"name"`
	Status    string          `json:"status"`
	Error     json.RawMessage `json:"error"`
	Detail    json.RawMessage `json:"detail"`
	Items     []TodoItem      `json:"items"`
	Trigger   string          `json:"trigger"`
	Kind      string          `json:"kind"`
	PluginID  string          `json:"pluginId"`
}

type TodoItem struct {
	Text       string `json:"text"`
	Completed  bool   `json:"completed"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm"`
}

type ToolDetail struct {
	Type        string   `json:"type"`
	Command     string   `json:"command"`
	Output      string   `json:"output"`
	ExitCode    *int     `json:"exitCode"`
	FilePath    string   `json:"filePath"`
	Content     string   `json:"content"`
	UnifiedDiff string   `json:"unifiedDiff"`
	OldString   string   `json:"oldString"`
	NewString   string   `json:"newString"`
	Query       string   `json:"query"`
	ToolName    string   `json:"toolName"`
	NumFiles    *int     `json:"numFiles"`
	NumMatches  *int     `json:"numMatches"`
	URL         string   `json:"url"`
	Label       string   `json:"label"`
	Text        string   `json:"text"`
	Description string   `json:"description"`
	SubAgent    string   `json:"subAgentType"`
	Log         string   `json:"log"`
	Worktree    string   `json:"worktreePath"`
	Branch      string   `json:"branchName"`
	FilePaths   []string `json:"filePaths"`
}

type Entry struct {
	Kind      string // user, assistant, reasoning, tool, todo, error, notice, compaction
	Text      string
	MessageID string
	Level     string
	Tool      *ToolEntry
	Todos     []TodoItem
	rev       int // bumps when content changes, used for render caching
}

type ToolEntry struct {
	CallID string
	Name   string
	Status string
	Error  string
	Detail ToolDetail
}

type Timeline struct {
	Entries []*Entry
	byCall  map[string]*Entry
}

func NewTimeline() *Timeline { return &Timeline{byCall: map[string]*Entry{}} }

func (t *Timeline) last() *Entry {
	if len(t.Entries) == 0 {
		return nil
	}
	return t.Entries[len(t.Entries)-1]
}

func (t *Timeline) push(e *Entry) { t.Entries = append(t.Entries, e) }

// Apply merges one raw timeline item into the timeline.
func (t *Timeline) Apply(raw json.RawMessage) {
	var it rawItem
	if err := json.Unmarshal(raw, &it); err != nil {
		return
	}
	switch it.Type {
	case "user_message":
		t.push(&Entry{Kind: "user", Text: it.Text, MessageID: it.MessageID})
	case "assistant_message", "reasoning":
		kind := "assistant"
		if it.Type == "reasoning" {
			kind = "reasoning"
		}
		if l := t.last(); l != nil && l.Kind == kind && (it.MessageID == "" || l.MessageID == "" || l.MessageID == it.MessageID) {
			l.Text += it.Text
			if l.MessageID == "" {
				l.MessageID = it.MessageID
			}
			l.rev++
			return
		}
		t.push(&Entry{Kind: kind, Text: it.Text, MessageID: it.MessageID})
	case "tool_call":
		var d ToolDetail
		_ = json.Unmarshal(it.Detail, &d)
		errText := ""
		if len(it.Error) > 0 && string(it.Error) != "null" {
			errText = toolErrorText(it.Error)
		}
		if e, ok := t.byCall[it.CallID]; ok && it.CallID != "" {
			e.Tool.Status = it.Status
			e.Tool.Error = errText
			e.Tool.Name = it.Name
			mergeDetail(&e.Tool.Detail, d)
			e.rev++
			return
		}
		e := &Entry{Kind: "tool", Tool: &ToolEntry{CallID: it.CallID, Name: it.Name, Status: it.Status, Error: errText, Detail: d}}
		t.push(e)
		if it.CallID != "" {
			t.byCall[it.CallID] = e
		}
	case "todo":
		// Keep a single, latest todo list in place.
		for _, e := range t.Entries {
			if e.Kind == "todo" {
				e.Todos = it.Items
				e.rev++
				return
			}
		}
		t.push(&Entry{Kind: "todo", Todos: it.Items})
	case "error":
		t.push(&Entry{Kind: "error", Text: it.Message})
	case "notification":
		t.push(&Entry{Kind: "notice", Text: it.Message, Level: it.Level})
	case "compaction":
		if it.Status == "completed" {
			t.push(&Entry{Kind: "notice", Text: "context compacted", Level: "info"})
		}
	}
}

// mergeDetail keeps earlier fields when a later update omits them.
func mergeDetail(dst *ToolDetail, src ToolDetail) {
	if src.Type == "" {
		return
	}
	old := *dst
	*dst = src
	if dst.Command == "" {
		dst.Command = old.Command
	}
	if dst.FilePath == "" {
		dst.FilePath = old.FilePath
	}
	if dst.Query == "" {
		dst.Query = old.Query
	}
	if dst.URL == "" {
		dst.URL = old.URL
	}
	if dst.Description == "" {
		dst.Description = old.Description
	}
}

// ---------- rendering ----------

var (
	cAccent  = lipgloss.AdaptiveColor{Light: "#5b4bdb", Dark: "#a99bff"}
	cMuted   = lipgloss.AdaptiveColor{Light: "#6b6b6b", Dark: "#8a8a8a"}
	cGreen   = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#5fd38d"}
	cYellow  = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#e5c07b"}
	cRed     = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#ff7b72"}
	cBlue    = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#79c0ff"}
	cBorder  = lipgloss.AdaptiveColor{Light: "#c8c8c8", Dark: "#3a3a3a"}
	cSelBg   = lipgloss.AdaptiveColor{Light: "#e8e4ff", Dark: "#2c2840"}
	sMuted   = lipgloss.NewStyle().Foreground(cMuted)
	sAccent  = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	sGreen   = lipgloss.NewStyle().Foreground(cGreen)
	sYellow  = lipgloss.NewStyle().Foreground(cYellow)
	sRed     = lipgloss.NewStyle().Foreground(cRed)
	sBlue    = lipgloss.NewStyle().Foreground(cBlue)
	sBold    = lipgloss.NewStyle().Bold(true)
	sItalic  = lipgloss.NewStyle().Foreground(cMuted).Italic(true)
	sUserBar = lipgloss.NewStyle().Border(lipgloss.ThickBorder(), false, false, false, true).BorderForeground(cAccent).PaddingLeft(1)
)

type Renderer struct {
	width    int
	md       *glamour.TermRenderer
	cache    map[*Entry]cached
	ShowThk  bool
	Verbose  bool
	darkMode bool
}

type cached struct {
	rev, width int
	thk, verb  bool
	out        string
}

func NewRenderer(dark bool) *Renderer {
	return &Renderer{cache: map[*Entry]cached{}, darkMode: dark}
}

func (r *Renderer) SetWidth(w int) {
	if w == r.width && r.md != nil {
		return
	}
	r.width = w
	style := styles.DarkStyleConfig
	if !r.darkMode {
		style = styles.LightStyleConfig
	}
	// glamour pads inline code with spaces it doesn't count when wrapping,
	// which strands single words on their own line; drop that padding.
	style.Code.Prefix, style.Code.Suffix = "", ""
	md, err := glamour.NewTermRenderer(glamour.WithStyles(style), glamour.WithWordWrap(0), glamour.WithEmoji())
	if err == nil {
		r.md = md
	}
	r.cache = map[*Entry]cached{}
}

func (r *Renderer) Reset() { r.cache = map[*Entry]cached{} }

func (r *Renderer) Render(t *Timeline) string {
	var b strings.Builder
	for i, e := range t.Entries {
		s := r.entry(e)
		if s == "" {
			continue
		}
		if i > 0 && b.Len() > 0 {
			// tools stack tightly; everything else gets breathing room
			if !(e.Kind == "tool" && t.Entries[i-1].Kind == "tool") {
				b.WriteString("\n")
			}
		}
		b.WriteString(s)
		b.WriteString("\n")
	}
	return b.String()
}

func (r *Renderer) entry(e *Entry) string {
	if c, ok := r.cache[e]; ok && c.rev == e.rev && c.width == r.width && c.thk == r.ShowThk && c.verb == r.Verbose {
		return c.out
	}
	out := r.renderEntry(e)
	r.cache[e] = cached{rev: e.rev, width: r.width, thk: r.ShowThk, verb: r.Verbose, out: out}
	return out
}

func (r *Renderer) wrap(s string, indent int) string {
	return lipgloss.NewStyle().Width(max(10, r.width-indent)).Render(s)
}

func (r *Renderer) renderEntry(e *Entry) string {
	switch e.Kind {
	case "user":
		return sUserBar.Width(max(10, r.width-3)).Render(sBold.Render(strings.TrimSpace(e.Text)))
	case "assistant":
		txt := strings.TrimSpace(e.Text)
		if txt == "" {
			return ""
		}
		if r.md != nil {
			if out, err := r.md.Render(txt); err == nil {
				return rewrap(trimPadding(strings.Trim(out, "\n")), r.width-1)
			}
		}
		return r.wrap(txt, 0)
	case "reasoning":
		txt := strings.TrimSpace(e.Text)
		if txt == "" {
			return ""
		}
		if !r.ShowThk {
			return sItalic.Render("  ∴ thinking… ") + sMuted.Render(fmt.Sprintf("(%d chars, t to show)", len(txt)))
		}
		return sItalic.Width(max(10, r.width-2)).PaddingLeft(2).Render("∴ " + txt)
	case "tool":
		return r.renderTool(e.Tool)
	case "todo":
		var b strings.Builder
		b.WriteString(sAccent.Render("  ☰ Tasks") + "\n")
		for _, it := range e.Todos {
			mark, st := "○", sMuted
			text := it.Text
			switch {
			case it.Completed || it.Status == "completed":
				mark, st = "✓", sGreen
			case it.Status == "in_progress":
				mark, st = "◐", sYellow
				if it.ActiveForm != "" {
					text = it.ActiveForm
				}
			}
			b.WriteString("    " + st.Render(mark) + " " + text + "\n")
		}
		return strings.TrimRight(b.String(), "\n")
	case "error":
		return sRed.Render("  ✗ ") + sRed.Width(max(10, r.width-4)).Render(e.Text)
	case "notice":
		st := sMuted
		if e.Level == "warning" {
			st = sYellow
		} else if e.Level == "error" {
			st = sRed
		}
		return st.Render("  · " + e.Text)
	}
	return ""
}

func toolIcon(status string) string {
	switch status {
	case "running":
		return sYellow.Render("●")
	case "completed":
		return sGreen.Render("✓")
	case "failed":
		return sRed.Render("✗")
	case "canceled":
		return sMuted.Render("○")
	}
	return sMuted.Render("·")
}

func (r *Renderer) renderTool(t *ToolEntry) string {
	d := t.Detail
	label, arg := t.Name, ""
	var body string
	switch d.Type {
	case "shell":
		label, arg = "Bash", d.Command
		if d.ExitCode != nil && *d.ExitCode != 0 {
			arg += sRed.Render(fmt.Sprintf("  exit %d", *d.ExitCode))
		}
		if r.Verbose {
			body = tailLines(d.Output, 12)
		}
	case "read":
		label, arg = "Read", d.FilePath
	case "write":
		label, arg = "Write", d.FilePath
		if r.Verbose {
			body = headLines(d.Content, 12)
		}
	case "edit":
		label, arg = "Edit", d.FilePath
		if r.Verbose {
			body = colorDiff(headLines(d.UnifiedDiff, 30))
		}
	case "search":
		label = "Search"
		if d.ToolName != "" {
			label = strings.ToUpper(d.ToolName[:1]) + d.ToolName[1:]
		}
		arg = d.Query
		if d.NumFiles != nil {
			arg += sMuted.Render(fmt.Sprintf("  %d files", *d.NumFiles))
		} else if d.NumMatches != nil {
			arg += sMuted.Render(fmt.Sprintf("  %d matches", *d.NumMatches))
		}
	case "fetch":
		label, arg = "Fetch", d.URL
	case "sub_agent":
		label = "Agent"
		if d.SubAgent != "" {
			label = "Agent:" + d.SubAgent
		}
		arg = d.Description
	case "worktree_setup":
		label, arg = "Worktree", d.Branch+" → "+d.Worktree
	case "plain_text":
		if d.Label != "" {
			arg = d.Label
		}
		if r.Verbose {
			body = headLines(d.Text, 8)
		}
	case "plan":
		label = "Plan"
		body = d.Text
	}
	arg = strings.ReplaceAll(homeRelative(arg), "\n", " ⏎ ")
	head := "  " + toolIcon(t.Status) + " " + sBlue.Bold(true).Render(label) + " " + arg
	head = lipgloss.NewStyle().MaxWidth(r.width).Render(head)
	var b strings.Builder
	b.WriteString(head)
	if t.Error != "" && t.Status == "failed" {
		b.WriteString("\n" + sRed.Render("    └ "+truncate(firstLine(t.Error), r.width-8)))
	}
	if body = strings.TrimRight(body, "\n"); body != "" {
		for _, l := range strings.Split(body, "\n") {
			b.WriteString("\n" + sMuted.Render("    │ ") + truncate(l, r.width-6))
		}
	}
	return b.String()
}

var trailingPad = regexp.MustCompile(`(?:[ \t]|\x1b\[[0-9;]*m)+$`)

// trimPadding drops the trailing spaces glamour pads every line with, which
// otherwise push lines past the viewport width and wrap them a second time.
func trimPadding(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = trailingPad.ReplaceAllString(l, "") + "\x1b[0m"
	}
	return strings.Join(lines, "\n")
}

// rewrap word-wraps rendered (ANSI) markdown, giving continuation lines the
// same indentation as the line they came from.
func rewrap(s string, width int) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		plain := ansi.Strip(line)
		lead := len(plain) - len(strings.TrimLeft(plain, " "))
		// list items hang under their text, not their bullet
		rest := plain[lead:]
		if m := listMarker.FindString(rest); m != "" {
			lead += ansi.StringWidth(m)
		}
		if ansi.StringWidth(line) <= width || lead >= width/2 {
			out = append(out, line)
			continue
		}
		parts := strings.Split(ansi.Wrap(line, width-lead, ""), "\n")
		out = append(out, parts[0])
		for _, p := range parts[1:] {
			out = append(out, strings.Repeat(" ", lead)+strings.TrimLeft(p, " "))
		}
	}
	return strings.Join(out, "\n")
}

var listMarker = regexp.MustCompile(`^(?:[•\-*]|\d+\.) `)

func colorDiff(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---"):
			lines[i] = sMuted.Render(l)
		case strings.HasPrefix(l, "+"):
			lines[i] = sGreen.Render(l)
		case strings.HasPrefix(l, "-"):
			lines[i] = sRed.Render(l)
		case strings.HasPrefix(l, "@@"):
			lines[i] = sBlue.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		return sMuted.Render(fmt.Sprintf("… %d more lines", len(lines)-n)) + "\n" + strings.Join(lines[len(lines)-n:], "\n")
	}
	return strings.Join(lines, "\n")
}

func headLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		return strings.Join(lines[:n], "\n") + "\n" + sMuted.Render(fmt.Sprintf("… %d more lines", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, w int) string {
	if w < 4 {
		w = 4
	}
	return ansi.Truncate(strings.ReplaceAll(s, "\t", "    "), w, "…")
}

// homeRelative shows paths under the home directory as ~/…: shorter, and it
// keeps usernames out of screenshots.
func homeRelative(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || len(home) < 2 {
		return s
	}
	return strings.ReplaceAll(s, home, "~")
}

// toolErrorText pulls a readable message out of a tool error, which may be a
// string, {message}, or a provider tool_result ({content: "Exit code 1\n…"}).
func toolErrorText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		for _, k := range []string{"message", "error", "content"} {
			if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		// content can also be a list of {type: "text", text}
		if parts, ok := m["content"].([]any); ok {
			for _, p := range parts {
				if pm, ok := p.(map[string]any); ok {
					if t, ok := pm["text"].(string); ok && t != "" {
						return t
					}
				}
			}
		}
	}
	return string(raw)
}
