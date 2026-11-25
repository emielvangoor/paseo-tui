package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Questions an agent asks (permission requests of kind "question", e.g. Claude's
// AskUserQuestion or Oh My Pi's ask): enter opens a form like the desktop's
// question card, one question at a time; the answers go back as the request's
// input plus an "answers" map keyed by each question's header.

type questionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type question struct {
	Question    string           `json:"question"`
	Header      string           `json:"header"`
	Options     []questionOption `json:"options"`
	MultiSelect bool             `json:"multiSelect"`
	AllowOther  bool             `json:"allowOther"`
	IsOther     bool             `json:"isOther"`
	AllowEmpty  bool             `json:"allowEmpty"`
	Placeholder string           `json:"placeholder"`
}

// showsText: a free-text answer is possible (no options, or "Other" allowed).
func (q question) showsText() bool { return len(q.Options) == 0 || q.AllowOther || q.IsOther }

// parseQuestions reads a question request's input, as the desktop does; nil
// when it isn't a well-formed list of questions.
func parseQuestions(input json.RawMessage) []question {
	var in struct {
		Questions []json.RawMessage `json:"questions"`
	}
	if json.Unmarshal(input, &in) != nil || len(in.Questions) == 0 {
		return nil
	}
	var out []question
	for _, raw := range in.Questions {
		var shape struct {
			Question *string           `json:"question"`
			Header   *string           `json:"header"`
			Options  []json.RawMessage `json:"options"`
		}
		var q question
		if json.Unmarshal(raw, &shape) != nil || shape.Question == nil || shape.Header == nil || shape.Options == nil ||
			json.Unmarshal(raw, &q) != nil {
			return nil
		}
		for _, o := range q.Options {
			if o.Label == "" {
				return nil
			}
		}
		out = append(out, q)
	}
	return out
}

type questionForm struct {
	agentID, requestID string
	input              json.RawMessage
	qs                 []question
	cur                int               // the question shown
	row                []int             // cursor row per question: options, then the free-text row
	picked             []map[int]bool    // chosen options per question
	text               []textinput.Model // free-text answer per question
	busy               bool
}

// pendingQuestion is the agent's first pending request when it's a question
// the form can show.
func (m Model) pendingQuestion(agentID string) (PermissionReq, []question, bool) {
	if perms := m.perms[agentID]; len(perms) > 0 && perms[0].Kind == "question" {
		if qs := parseQuestions(perms[0].Input); qs != nil {
			return perms[0], qs, true
		}
	}
	return PermissionReq{}, nil, false
}

func (m *Model) openQuestion(agentID string, p PermissionReq, qs []question) (tea.Model, tea.Cmd) {
	f := &questionForm{agentID: agentID, requestID: p.ID, input: p.Input, qs: qs,
		row: make([]int, len(qs)), picked: make([]map[int]bool, len(qs)), text: make([]textinput.Model, len(qs))}
	for i, q := range qs {
		f.picked[i] = map[int]bool{}
		f.text[i] = pfInput(orDefault(q.Placeholder, "Type your answer…"), "")
	}
	m.question, m.overlay = f, ovQuestion
	if m.focus == focusInput {
		m.focus, m.slash = focusList, nil
		m.input.Blur()
	}
	return *m, f.focusText()
}

// onText: the cursor is on the free-text row of the current question.
func (f *questionForm) onText() bool {
	q := f.qs[f.cur]
	return q.showsText() && f.row[f.cur] == len(q.Options)
}

func (f *questionForm) focusText() tea.Cmd {
	for i := range f.text {
		f.text[i].Blur()
	}
	if f.onText() {
		return f.text[f.cur].Focus()
	}
	return nil
}

func (f *questionForm) answered(i int) bool {
	q := f.qs[i]
	if len(f.picked[i]) > 0 {
		return true
	}
	if !q.showsText() {
		return false
	}
	return strings.TrimSpace(f.text[i].Value()) != "" || q.AllowEmpty
}

// answers mirrors the desktop: chosen labels joined by ", ", a typed answer
// replacing them for single choice and added to them for multiple choice.
func (f *questionForm) answers() map[string]string {
	out := map[string]string{}
	for i, q := range f.qs {
		var labels []string
		for j, o := range q.Options {
			if f.picked[i][j] {
				labels = append(labels, o.Label)
			}
		}
		if q.showsText() {
			if t := strings.TrimSpace(f.text[i].Value()); t != "" {
				if q.MultiSelect {
					out[q.Header] = strings.Join(append(labels, t), ", ")
				} else {
					out[q.Header] = t
				}
				continue
			}
			if q.AllowEmpty && len(q.Options) == 0 {
				out[q.Header] = ""
				continue
			}
		}
		if len(labels) > 0 {
			out[q.Header] = strings.Join(labels, ", ")
		}
	}
	return out
}

// respondQuestion sends allow with the answers merged into the request's input.
func (m *Model) respondQuestion(agentID, requestID string, input json.RawMessage, answers map[string]string, what string) tea.Cmd {
	in := map[string]any{}
	_ = json.Unmarshal(input, &in)
	in["answers"] = answers
	return m.call(what, "permit", map[string]any{"agentId": agentID, "requestId": requestID,
		"response": map[string]any{"behavior": "allow", "updatedInput": in}})
}

// dismissQuestion declines a question (x); an all-optional text question is
// answered empty instead, as the desktop's Skip does.
func (m *Model) dismissQuestion(agentID string, p PermissionReq, qs []question) tea.Cmd {
	skip := len(qs) > 0
	for _, q := range qs {
		skip = skip && q.AllowEmpty && len(q.Options) == 0
	}
	if skip {
		answers := map[string]string{}
		for _, q := range qs {
			answers[q.Header] = ""
		}
		return m.respondQuestion(agentID, p.ID, p.Input, answers, "Skipped the question")
	}
	return m.call("Dismissed the question", "permit", map[string]any{"agentId": agentID, "requestId": p.ID,
		"response": map[string]any{"behavior": "deny", "message": "Dismissed by user"}})
}

func (m *Model) questionKey(k tea.KeyMsg) tea.Cmd {
	f := m.question
	q := f.qs[f.cur]
	rows := len(q.Options)
	if q.showsText() {
		rows++
	}
	next := func() tea.Cmd { // the next unanswered question, else send
		for i := 1; i <= len(f.qs); i++ {
			if j := (f.cur + i) % len(f.qs); !f.answered(j) {
				f.cur = j
				return f.focusText()
			}
		}
		m.overlay, m.question = ovNone, nil
		return m.respondQuestion(f.agentID, f.requestID, f.input, f.answers(), "Answered")
	}
	switch k.String() {
	case "esc":
		m.overlay, m.question = ovNone, nil // answer later: enter opens it again
		return nil
	case "tab", "shift+tab":
		d := map[string]int{"tab": 1, "shift+tab": -1}[k.String()]
		f.cur = (f.cur + d + len(f.qs)) % len(f.qs)
		return f.focusText()
	case "up", "down":
		d := map[string]int{"up": -1, "down": 1}[k.String()]
		f.row[f.cur] = (f.row[f.cur] + d + rows) % rows
		return f.focusText()
	case "enter":
		if f.onText() {
			if !f.answered(f.cur) {
				return nil
			}
			if !q.MultiSelect {
				f.picked[f.cur] = map[int]bool{} // a typed answer replaces a choice
			}
			return next()
		}
		r := f.row[f.cur]
		if q.MultiSelect {
			if len(f.picked[f.cur]) == 0 {
				f.picked[f.cur][r] = true
			}
		} else {
			f.picked[f.cur] = map[int]bool{r: true}
			f.text[f.cur].SetValue("")
		}
		return next()
	case " ":
		if !f.onText() {
			r := f.row[f.cur]
			if q.MultiSelect {
				f.picked[f.cur][r] = !f.picked[f.cur][r]
				if !f.picked[f.cur][r] {
					delete(f.picked[f.cur], r)
				}
			} else {
				f.picked[f.cur] = map[int]bool{r: true}
			}
			return nil
		}
	}
	if f.onText() {
		var c tea.Cmd
		f.text[f.cur], c = f.text[f.cur].Update(k)
		return c
	}
	if k.Type == tea.KeyRunes && q.showsText() { // typing goes to the free-text answer
		f.row[f.cur] = len(q.Options)
		c := f.focusText()
		f.text[f.cur], _ = f.text[f.cur].Update(k)
		return c
	}
	return nil
}

func (m Model) questionView() string {
	f := m.question
	q := f.qs[f.cur]
	w := max(50, min(100, m.width-8))
	inner := w - 4
	title := " Question"
	if len(f.qs) > 1 {
		title += fmt.Sprintf("  %d of %d", f.cur+1, len(f.qs))
	}
	lines := []string{sAccent.Render(title), ""}
	if q.Header != "" && q.Header != q.Question {
		lines = append(lines, " "+sMuted.Render(truncate(q.Header, inner-2)))
	}
	lines = append(lines, lipgloss.NewStyle().Width(inner-1).Render(" "+sBold.Render(q.Question)), "")
	for j, o := range q.Options {
		mark := "( )"
		if q.MultiSelect {
			mark = "[ ]"
		}
		if f.picked[f.cur][j] {
			mark = map[bool]string{true: "[x]", false: "(•)"}[q.MultiSelect]
		}
		line := " " + mark + " " + o.Label
		if o.Description != "" {
			line += sMuted.Render("  " + o.Description)
		}
		line = truncate(line, inner-2)
		if f.row[f.cur] == j {
			line = fillBg(sAccent.Render("▌")+line[1:], inner, cSelBg)
		}
		lines = append(lines, line)
	}
	if q.showsText() {
		f.text[f.cur].Width = inner - 12
		label := "  Other: "
		if len(q.Options) == 0 {
			label = "  Answer: "
		}
		if f.onText() {
			label = sAccent.Render("▌") + label[1:]
		}
		lines = append(lines, label+f.text[f.cur].View())
	}
	help := "↑↓ choose · enter pick and continue · esc later · x (outside) dismiss"
	if q.MultiSelect {
		help = "↑↓ move · space toggle · enter continue · esc later · x (outside) dismiss"
	}
	if len(f.qs) > 1 {
		help = "tab next question · " + help
	}
	lines = append(lines, "", sMuted.Render(" "+help))
	return frame(lines, w)
}
