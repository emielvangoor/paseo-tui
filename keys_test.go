package main

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// ctrl+n / ctrl+p move the selection like down / up; they don't close the palette.
func TestPaletteCtrlNP(t *testing.T) {
	p := &Palette{rows: []paletteRow{{header: "Actions"}, {action: &paletteAction{}}, {action: &paletteAction{}}}, sel: 1}
	if done, _, _ := p.Update(tea.KeyMsg{Type: tea.KeyCtrlN}); done || p.sel != 2 {
		t.Fatalf("ctrl+n: done=%v sel=%d, want false 2", done, p.sel)
	}
	if done, _, _ := p.Update(tea.KeyMsg{Type: tea.KeyCtrlP}); done || p.sel != 1 {
		t.Fatalf("ctrl+p: done=%v sel=%d, want false 1", done, p.sel)
	}
	if done, _, _ := p.Update(tea.KeyMsg{Type: tea.KeyEsc}); !done {
		t.Fatal("esc should close the palette")
	}
}

func TestTitleSafe(t *testing.T) {
	if got := titleSafe("fix\x1b]2;pwned\x07 bug\n", 80); got != "fix]2;pwned bug" {
		t.Fatalf("control characters kept: %q", got)
	}
	long := titleSafe(strings.Repeat("é", 100), 80)
	if n := len([]rune(long)); n != 81 {
		t.Fatalf("length %d, want 80 + ellipsis", n)
	}
}

// Keys reach a floating pane's program as the bytes a real terminal would send.
func TestPaneKeySeq(t *testing.T) {
	cases := []struct {
		k    tea.KeyMsg
		want string
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é")}, "é"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true}, "\x1bx"},
		{tea.KeyMsg{Type: tea.KeyCtrlX}, "\x18"},
		{tea.KeyMsg{Type: tea.KeyEsc}, "\x1b"},
		{tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z"},
		{tea.KeyMsg{Type: tea.KeyCtrlLeft}, "\x1b[1;5D"},
		{tea.KeyMsg{Type: tea.KeyCtrlShiftUp}, "\x1b[1;6A"},
		{tea.KeyMsg{Type: tea.KeyShiftEnd}, "\x1b[1;2F"},
		{tea.KeyMsg{Type: tea.KeyCtrlPgDown}, "\x1b[6;5~"},
	}
	for _, c := range cases {
		if seq, _, ok := paneKeySeq(c.k); !ok || seq != c.want {
			t.Errorf("%s: got %q (ok=%v), want %q", c.k, seq, ok, c.want)
		}
	}
	// plain cursor keys are left to the emulator, which knows the cursor-key mode
	if _, code, ok := paneKeySeq(tea.KeyMsg{Type: tea.KeyUp}); ok || code == 0 {
		t.Errorf("up: ok=%v code=%d, want the key code", ok, code)
	}
}

func TestActiveSlash(t *testing.T) {
	cases := []struct {
		text  string
		start int
		query string
		ok    bool
	}{
		{"/rev", 0, "rev", true},
		{"/", 0, "", true},
		{"fix it /sk", 7, "sk", true},
		{"a/b", 0, "", false},         // not after a space
		{"/review now", 0, "", false}, // the cursor is past the command
		{"say \"/x", 0, "", false},    // quoted
	}
	for _, c := range cases {
		line := []rune(c.text)
		start, query, ok := activeSlash(line, len(line))
		if ok != c.ok || (ok && (start != c.start || query != c.query)) {
			t.Errorf("%q: got (%d, %q, %v), want (%d, %q, %v)", c.text, start, query, ok, c.start, c.query, c.ok)
		}
	}
	cmds := []slashCommand{{Name: "review"}, {Name: "skill:preview", Kind: "skill"}, {Name: "rev"}}
	got := rankCommands(cmds, "rev", false)
	if len(got) != 3 || got[0].Name != "rev" || got[1].Name != "review" {
		t.Errorf("ranking: %+v", got)
	}
	if got := rankCommands(cmds, "", true); len(got) != 1 || got[0].Name != "skill:preview" {
		t.Errorf("inline lists only skills: %+v", got)
	}
}

// Answers go back keyed by header: a choice's label, a typed answer replacing it
// for single choice and joining the choices for multiple choice.
func TestQuestionAnswers(t *testing.T) {
	qs := parseQuestions(json.RawMessage(`{"questions":[
		{"question":"Pick a color","header":"Color","options":[{"label":"Red"},{"label":"Green"}],"allowOther":true},
		{"question":"Toppings?","header":"Toppings","multiSelect":true,"options":[{"label":"Cheese"},{"label":"Ham"}],"allowOther":true},
		{"question":"Anything else?","header":"Note","options":[],"allowEmpty":true}]}`))
	if len(qs) != 3 {
		t.Fatalf("parsed %d questions", len(qs))
	}
	m := Model{perms: map[string][]PermissionReq{}}
	mm, _ := m.openQuestion("a", PermissionReq{ID: "r"}, qs)
	f := mm.(Model).question
	f.picked[0][1] = true
	f.picked[1][0], f.picked[1][1] = true, true
	f.text[1].SetValue("Pineapple")
	got := f.answers()
	want := map[string]string{"Color": "Green", "Toppings": "Cheese, Ham, Pineapple", "Note": ""}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	if parseQuestions(json.RawMessage(`{"questions":[{"question":"x"}]}`)) != nil {
		t.Error("a question without header and options should not parse")
	}
}

// /clear and /exit (and their aliases) are recognised only as a whole message,
// listed after the agent's own commands, and win over an agent command of the same name.
func TestClientCommands(t *testing.T) {
	for text, want := range map[string]string{"/clear": "clear", " /new ": "clear", "/q": "exit", "/quit": "exit",
		"/clear now": "", "please /clear": "", "/clearly": "", "clear": ""} {
		got := ""
		if c := clientCommand(text); c != nil {
			got = c.Name
		}
		if got != want {
			t.Errorf("%q: got %q, want %q", text, got, want)
		}
	}
	list := withClientCommands([]slashCommand{{Name: "clear", Kind: "command"}, {Name: "review"}})
	if len(list) != 3 || list[0].Name != "review" || list[1].Kind != "client" {
		t.Errorf("merged list: %+v", list)
	}
	if got := rankCommands(list, "qui", false); len(got) != 1 || got[0].Name != "exit" {
		t.Errorf("alias match: %+v", got)
	}
}

// An Oh My Pi multiple-choice round ("(1 selected) …" with "Other" and "Done
// selecting") reads as one checkbox question with an own-answer field.
func TestQuestionBoxListsOptions(t *testing.T) {
	in := json.RawMessage(`{"questions":[{"question":"(1 selected) Pick colors","header":"Response","options":[
		{"label":"Red"},{"label":"Green"},{"label":"Blue"},{"label":"Other (type your own)"},{"label":"[+] Done selecting"}]}]}`)
	meta := json.RawMessage(`{"extensionUiMethod":"select","answerHeader":"Response"}`)
	m := Model{perms: map[string][]PermissionReq{"a": {{ID: "r", Kind: "question", Input: in, Metadata: meta}}}}
	_, qs, _ := m.pendingQuestion("a")
	if q := qs[0]; !q.MultiSelect || !q.AllowOther || len(q.Options) != 3 || q.Question != "Pick colors" || q.omp.done != "[+] Done selecting" {
		t.Fatalf("round read as %+v", q)
	}
	box := ansi.Strip(m.permBox(Agent{ID: "a"}, 100))
	for _, want := range []string{"Pick colors", "☐ Red", "☐ Blue", "Other: your own answer"} {
		if !strings.Contains(box, want) {
			t.Errorf("box lacks %q:\n%s", want, box)
		}
	}
	if strings.Contains(box, "Done selecting") || strings.Contains(box, "(1 selected)") {
		t.Errorf("box shows omp's round mechanics:\n%s", box)
	}
}

// An answered Oh My Pi multiple choice is sent round by round: each pick, then
// "Other" and its text, then "Done selecting"; a round of another question is left alone.
func TestAskPlanRounds(t *testing.T) {
	meta := func(method string) json.RawMessage { return json.RawMessage(`{"extensionUiMethod":"` + method + `"}`) }
	round := func(id, method, title string) PermissionReq {
		in := `{"questions":[{"question":"` + title + `","header":"Response","options":[]}]}`
		return PermissionReq{ID: id, Kind: "question", Title: title, Input: json.RawMessage(in), Metadata: meta(method)}
	}
	plan := &askPlan{title: "Pick colors", picks: []string{"Red", "Blue"}, custom: "purple", done: "[+] Done selecting", answered: map[string]bool{}}
	m := Model{perms: map[string][]PermissionReq{}, askPlans: map[string]*askPlan{"a": plan}}
	steps := []struct {
		req   PermissionReq
		check func() bool
	}{
		{round("1", "select", "Pick colors"), func() bool { return len(plan.picks) == 1 }},
		{round("2", "select", "(1 selected) Pick colors"), func() bool { return len(plan.picks) == 0 && !plan.customSent }},
		{round("3", "select", "(2 selected) Pick colors"), func() bool { return plan.customSent && plan.custom == "purple" }},
		{round("4", "input", "Pick colors"), func() bool { return plan.custom == "" && !plan.finished }},
		{round("5", "select", "(3 selected) Pick colors"), func() bool { return plan.finished }},
	}
	for i, s := range steps {
		m.perms["a"] = []PermissionReq{s.req}
		if m.driveAsk("a") == nil || !s.check() {
			t.Fatalf("round %d (%s %q) not answered as planned: %+v", i+1, s.req.Metadata, s.req.Title, plan)
		}
		if m.driveAsk("a") != nil {
			t.Fatalf("round %d answered twice", i+1)
		}
	}
	other := &askPlan{title: "Pick colors", picks: []string{"Red"}, answered: map[string]bool{}}
	m.askPlans["a"] = other
	m.perms["a"] = []PermissionReq{round("9", "select", "Something else")}
	if m.driveAsk("a") != nil || m.askPlans["a"] != nil {
		t.Fatal("a different question was answered from the plan")
	}
}
