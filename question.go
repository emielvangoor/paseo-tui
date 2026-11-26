package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Questions an agent asks (permission requests of kind "question", e.g. Claude's
// AskUserQuestion or Oh My Pi's ask): enter opens one scrollable form with every
// question of the request, its options, and a field for an own answer. The
// answers go back as the request's input plus an "answers" map keyed by each
// question's header.
//
// Oh My Pi asks a form one field at a time and a multiple choice one pick per
// round ("(1 selected) …", then "[+] Done selecting"; "Other" is followed by a
// text prompt). Paseo passes those rounds through as separate requests, so the
// form shows the field as one list of checkboxes and paseo-tui answers the
// rounds itself (askPlan).

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
	omp         *ompRound        // set for an Oh My Pi select round
}

// ompRound is one select round of an Oh My Pi question.
type ompRound struct {
	title string // the field's title, without the "(N selected) " of later rounds
	done  string // the "[+] Done selecting" option of a multiple choice
}

const ompOther = "Other (type your own)"

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

// ompSelectRound turns an Oh My Pi select round into the question it stands
// for: its own options, with "Other" as the free-text field and "Done
// selecting" making it a multiple choice.
func ompSelectRound(p PermissionReq, q question) question {
	var meta struct {
		Method string `json:"extensionUiMethod"`
	}
	if json.Unmarshal(p.Metadata, &meta) != nil || meta.Method != "select" {
		return q
	}
	r := &ompRound{title: stripSelected(q.Question)}
	opts := q.Options[:0:0]
	for _, o := range q.Options {
		switch {
		case o.Label == ompOther:
			q.AllowOther = true
		case strings.HasSuffix(o.Label, " Done selecting"):
			r.done = o.Label
		default:
			opts = append(opts, o)
		}
	}
	q.Options, q.MultiSelect, q.Question, q.omp = opts, r.done != "", r.title, r
	return q
}

// stripSelected drops the "(2 selected) " Oh My Pi puts before later rounds' titles.
func stripSelected(title string) string {
	if rest, ok := strings.CutPrefix(title, "("); ok {
		if n, tail, ok := strings.Cut(rest, " selected) "); ok && strings.Trim(n, "0123456789") == "" {
			return tail
		}
	}
	return title
}

// pendingQuestion is the agent's first pending request when it's a question
// the form can show.
func (m Model) pendingQuestion(agentID string) (PermissionReq, []question, bool) {
	if perms := m.perms[agentID]; len(perms) > 0 && perms[0].Kind == "question" {
		if qs := parseQuestions(perms[0].Input); qs != nil {
			if len(qs) == 1 {
				qs[0] = ompSelectRound(perms[0], qs[0])
			}
			return perms[0], qs, true
		}
	}
	return PermissionReq{}, nil, false
}

// ---------- the form ----------

// qRow is a selectable row: an option (opt ≥ 0), a question's own-answer field
// (opt == -1), or the submit button (q == -1).
type qRow struct{ q, opt int }

type questionForm struct {
	agentID, requestID string
	input              json.RawMessage
	qs                 []question
	rows               []qRow
	cur                int               // index in rows
	top                int               // first body line shown
	picked             []map[int]bool    // chosen options per question
	text               []textinput.Model // own answer per question
	err                string
}

func (m *Model) openQuestion(agentID string, p PermissionReq, qs []question) (tea.Model, tea.Cmd) {
	f := &questionForm{agentID: agentID, requestID: p.ID, input: p.Input, qs: qs,
		picked: make([]map[int]bool, len(qs)), text: make([]textinput.Model, len(qs))}
	for i, q := range qs {
		f.picked[i] = map[int]bool{}
		placeholder := "type your own answer"
		if len(q.Options) == 0 {
			placeholder = "type your answer"
		}
		f.text[i] = pfInput(orDefault(q.Placeholder, placeholder), "")
		for j := range q.Options {
			f.rows = append(f.rows, qRow{i, j})
		}
		if q.showsText() {
			f.rows = append(f.rows, qRow{i, -1})
		}
	}
	f.rows = append(f.rows, qRow{-1, 0})
	m.question, m.overlay = f, ovQuestion
	if m.focus == focusInput {
		m.focus, m.slash = focusList, nil
		m.input.Blur()
	}
	return *m, f.focusText()
}

func (f *questionForm) row() qRow { return f.rows[f.cur] }

// focusText focuses the own-answer field under the cursor, if that's where it is.
func (f *questionForm) focusText() tea.Cmd {
	for i := range f.text {
		f.text[i].Blur()
	}
	if r := f.row(); r.q >= 0 && r.opt < 0 {
		return f.text[r.q].Focus()
	}
	return nil
}

func (f *questionForm) moveTo(i int) tea.Cmd {
	f.cur = min(max(0, i), len(f.rows)-1)
	return f.focusText()
}

// nextQuestion moves to the first row of the question after q (or the submit button).
func (f *questionForm) nextQuestion(q, dir int) tea.Cmd {
	target := q + dir
	if target < 0 {
		target = 0
	}
	for i, r := range f.rows {
		if r.q == target || (r.q == -1 && target >= len(f.qs)) {
			return f.moveTo(i)
		}
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
		labels := f.labels(i)
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

// labels are question i's chosen options, in the order they're listed.
func (f *questionForm) labels(i int) []string {
	var out []string
	for j, o := range f.qs[i].Options {
		if f.picked[i][j] {
			out = append(out, o.Label)
		}
	}
	return out
}

// choose picks option opt of question q: the only one for single choice, toggled for multiple.
func (f *questionForm) choose(q, opt int) {
	if f.qs[q].MultiSelect {
		if f.picked[q][opt] {
			delete(f.picked[q], opt)
		} else {
			f.picked[q][opt] = true
		}
		return
	}
	f.picked[q] = map[int]bool{opt: true}
	f.text[q].SetValue("") // a choice replaces a typed answer
}

func (m *Model) questionKey(k tea.KeyMsg) tea.Cmd {
	f := m.question
	r := f.row()
	f.err = ""
	switch k.String() {
	case "esc":
		m.overlay, m.question = ovNone, nil // answer later: enter opens it again
		return nil
	case "ctrl+s", "alt+enter":
		return m.submitQuestion()
	case "up", "ctrl+p":
		return f.moveTo(f.cur - 1)
	case "down", "ctrl+n":
		return f.moveTo(f.cur + 1)
	case "tab":
		return f.nextQuestion(r.q, 1) // from the send button, r.q is -1: back to the first question
	case "shift+tab":
		if r.q < 0 {
			return f.nextQuestion(len(f.qs), -1)
		}
		return f.nextQuestion(r.q, -1)
	case "pgdown":
		return f.moveTo(f.cur + 8)
	case "pgup":
		return f.moveTo(f.cur - 8)
	case "enter":
		switch {
		case r.q < 0:
			return m.submitQuestion()
		case r.opt < 0: // own answer: a typed answer replaces a single choice
			if strings.TrimSpace(f.text[r.q].Value()) != "" && !f.qs[r.q].MultiSelect {
				f.picked[r.q] = map[int]bool{}
			}
			return f.nextQuestion(r.q, 1)
		case f.qs[r.q].MultiSelect:
			f.choose(r.q, r.opt)
			return nil
		default:
			f.choose(r.q, r.opt)
			return f.nextQuestion(r.q, 1)
		}
	case " ":
		if r.q >= 0 && r.opt >= 0 {
			f.choose(r.q, r.opt)
			return nil
		}
	}
	if r.q < 0 {
		return nil
	}
	if r.opt < 0 { // typing in the own-answer field
		var c tea.Cmd
		f.text[r.q], c = f.text[r.q].Update(k)
		if strings.TrimSpace(f.text[r.q].Value()) != "" && !f.qs[r.q].MultiSelect {
			f.picked[r.q] = map[int]bool{}
		}
		return c
	}
	if (k.Type == tea.KeyRunes || k.Paste) && f.qs[r.q].showsText() { // typing on an option starts an own answer
		for i, rr := range f.rows {
			if rr.q == r.q && rr.opt < 0 {
				c := f.moveTo(i)
				f.text[r.q], _ = f.text[r.q].Update(k)
				if !f.qs[r.q].MultiSelect {
					f.picked[r.q] = map[int]bool{}
				}
				return c
			}
		}
	}
	return nil
}

// submitQuestion sends the answers, or points at the first unanswered question.
func (m *Model) submitQuestion() tea.Cmd {
	f := m.question
	for i := range f.qs {
		if !f.answered(i) {
			f.err = fmt.Sprintf("Answer question %d first", i+1)
			return f.nextQuestion(i, 0)
		}
	}
	m.overlay, m.question = ovNone, nil
	if q := f.qs[0]; len(f.qs) == 1 && q.omp != nil {
		plan := &askPlan{title: q.omp.title, picks: f.labels(0), custom: strings.TrimSpace(f.text[0].Value()),
			done: q.omp.done, answered: map[string]bool{}}
		if !q.MultiSelect && plan.custom != "" {
			plan.picks = nil
		}
		m.askPlans[f.agentID] = plan
		m.setStatus("Answering…", false)
		return m.driveAsk(f.agentID)
	}
	return m.respondQuestion(f.agentID, f.requestID, f.input, f.answers(), "Answered")
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
	delete(m.askPlans, agentID)
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

// ---------- answering Oh My Pi's rounds ----------

// askPlan is an answered Oh My Pi question whose rounds are still coming: each
// pick, then "Other" and its text, then "Done selecting".
type askPlan struct {
	title      string
	picks      []string
	custom     string
	customSent bool
	done       string          // "Done selecting" option; "" for a single choice
	answered   map[string]bool // requests already answered
	since      time.Time       // when the plan finished, to open the next field's question
	finished   bool
}

// driveAsk answers the agent's pending Oh My Pi round from its plan. When the
// plan is done and the agent asks its next field, that question opens by itself.
func (m *Model) driveAsk(agentID string) tea.Cmd {
	plan := m.askPlans[agentID]
	perms := m.perms[agentID]
	if plan == nil || len(perms) == 0 {
		return nil
	}
	p := perms[0]
	if plan.answered[p.ID] {
		return nil
	}
	var meta struct {
		Method string `json:"extensionUiMethod"`
	}
	_ = json.Unmarshal(p.Metadata, &meta)
	title := p.Title
	if qs := parseQuestions(p.Input); len(qs) > 0 {
		title = qs[0].Question
	}
	if plan.finished {
		// the next field of the same form: open it, as if the form went on
		delete(m.askPlans, agentID)
		if time.Since(plan.since) < 15*time.Second && m.overlay == ovNone && agentID == m.selectedID {
			if rp, qs, ok := m.pendingQuestion(agentID); ok {
				mm, c := m.openQuestion(agentID, rp, qs)
				*m = mm.(Model)
				return c
			}
		}
		return nil
	}
	value := ""
	switch {
	case meta.Method == "select" && stripSelected(title) == plan.title:
		switch {
		case len(plan.picks) > 0:
			value, plan.picks = plan.picks[0], plan.picks[1:]
		case plan.custom != "" && !plan.customSent:
			value, plan.customSent = ompOther, true
		case plan.done != "":
			value = plan.done
		}
	case (meta.Method == "input" || meta.Method == "editor") && title == plan.title && plan.customSent && plan.custom != "":
		value, plan.custom = plan.custom, ""
	}
	if value == "" { // not a round of this question: leave it to the user
		delete(m.askPlans, agentID)
		return nil
	}
	plan.answered[p.ID] = true
	last := value == plan.done || (plan.done == "" && len(plan.picks) == 0 && (plan.custom == "" || !plan.customSent) && value != ompOther)
	if last {
		plan.finished, plan.since = true, time.Now()
	}
	what := ""
	if last {
		what = "Answered"
	}
	return m.respondQuestion(agentID, p.ID, p.Input, map[string]string{"Response": value}, what)
}

// driveAsks runs driveAsk for every agent with a plan, after the agent list changed.
func (m *Model) driveAsks() tea.Cmd {
	var cmds []tea.Cmd
	for id := range m.askPlans {
		cmds = append(cmds, m.driveAsk(id))
	}
	return tea.Batch(cmds...)
}

// ---------- view ----------

func (m Model) questionView() string {
	f := m.question
	w := max(50, min(110, m.width-6))
	inner := w - 4
	var lines []string
	rowLine := map[int]int{} // row -> its line
	for i, q := range f.qs {
		if i > 0 {
			lines = append(lines, "")
		}
		head := ""
		if len(f.qs) > 1 {
			head = sAccent.Render(fmt.Sprintf("%d/%d ", i+1, len(f.qs)))
		}
		if q.Header != "" && q.Header != q.Question && q.Header != "Response" {
			head += sAccent.Render(q.Header) + " "
		}
		if f.answered(i) {
			head += sGreen.Render("✓")
		}
		if head != "" {
			lines = append(lines, head)
		}
		for _, l := range strings.Split(lipgloss.NewStyle().Width(inner).Render(strings.TrimSpace(q.Question)), "\n") {
			lines = append(lines, sBold.Render(l))
		}
		switch {
		case len(q.Options) == 0:
		case q.MultiSelect:
			lines = append(lines, sMuted.Render("pick any (space or enter toggles)"))
		default:
			lines = append(lines, sMuted.Render("pick one"))
		}
		for ri, r := range f.rows {
			if r.q != i {
				continue
			}
			sel := ri == f.cur
			cursor := "  "
			if sel {
				cursor = sAccent.Render("▌ ")
			}
			if r.opt < 0 {
				label := "Other: "
				if len(q.Options) == 0 {
					label = "Answer: "
				}
				mark := ""
				if len(q.Options) > 0 {
					mark = "( ) "
					if q.MultiSelect {
						mark = "[ ] "
					}
					if strings.TrimSpace(f.text[i].Value()) != "" {
						mark = map[bool]string{true: "[x] ", false: "(•) "}[q.MultiSelect]
					}
				}
				f.text[i].Width = max(10, inner-lipgloss.Width(label+mark)-4)
				rowLine[ri] = len(lines)
				lines = append(lines, cursor+mark+sMuted.Render(label)+f.text[i].View())
				continue
			}
			o := q.Options[r.opt]
			mark := "( ) "
			if q.MultiSelect {
				mark = "[ ] "
			}
			if f.picked[i][r.opt] {
				mark = map[bool]string{true: "[x] ", false: "(•) "}[q.MultiSelect]
			}
			label := o.Label
			if sel {
				label = sBold.Render(label)
			}
			line := cursor + mark + label
			if o.Description != "" && o.Description != o.Label {
				line += sMuted.Render("  " + o.Description)
			}
			rowLine[ri] = len(lines)
			line = truncate(line, inner)
			if sel {
				line = fillBg(line, inner, cSelBg)
			}
			lines = append(lines, line)
		}
	}
	lines = append(lines, "")
	submit := "  Send answers"
	if f.row().q < 0 {
		submit = fillBg(sAccent.Render("▌ ")+sBold.Render("Send answers"), inner, cSelBg)
	}
	rowLine[len(f.rows)-1] = len(lines)
	lines = append(lines, submit)

	// scroll so the cursor row shows, with its question's text when it fits
	room := max(5, m.height-8)
	at := rowLine[f.cur]
	if at < f.top {
		f.top = at
		for f.top > 0 && lines[f.top-1] != "" && at-f.top < room/2 { // reveal the question above its options
			f.top--
		}
	}
	if at >= f.top+room {
		f.top = at - room + 1
	}
	f.top = min(f.top, max(0, len(lines)-room))
	body := lines[f.top:min(len(lines), f.top+room)]

	title := sAccent.Render(" Question")
	if len(f.qs) > 1 {
		title = sAccent.Render(fmt.Sprintf(" %d questions", len(f.qs)))
	}
	if len(lines) > room {
		title += sMuted.Render(fmt.Sprintf("   %d%%", min(100, (f.top+room)*100/len(lines))))
	}
	out := []string{title, ""}
	out = append(out, body...)
	if f.err != "" {
		out = append(out, sRed.Render(" "+f.err))
	}
	help := "↑↓ move · enter pick · type for your own answer · ctrl+s send · esc later"
	if len(f.qs) > 1 {
		help = "↑↓ move · tab next question · enter pick · type for your own answer · ctrl+s send · esc later"
	}
	out = append(out, "", sMuted.Render(" "+truncate(help, inner)))
	return frame(out, w)
}
