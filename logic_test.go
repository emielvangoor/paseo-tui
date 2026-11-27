package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestFuzzyScore(t *testing.T) {
	cases := []struct {
		q, s string
		ok   bool
	}{
		{"auth", "fix the auth middleware", true},
		{"fam", "fix the auth middleware", false}, // letters too scattered
		{"fix auth", "fix the auth middleware", true},
		{"fix zebra", "fix the auth middleware", false}, // every word must match
		{"opus 5", "claude opus 5.5", true},
	}
	for _, c := range cases {
		if _, ok := fuzzyScore(c.q, c.s); ok != c.ok {
			t.Errorf("fuzzyScore(%q, %q) = %v, want %v", c.q, c.s, ok, c.ok)
		}
	}
	// a prefix/word-start match outranks a mid-word one
	a, _ := fuzzyScore("api", "api gateway")
	b, _ := fuzzyScore("api", "rapid prototype")
	if a <= b {
		t.Errorf("word-start match should score higher: %d <= %d", a, b)
	}
}

func TestImagePathsIn(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	spaced := filepath.Join(dir, "my shot.jpg")
	for _, p := range []string{png, spaced} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]int{
		png:                            1,
		fileURI(png):                   1,
		"'" + spaced + "'":             1,
		"look at " + png:               0, // ordinary text stays text
		filepath.Join(dir, "nope.png"): 0,
		"hello world":                  0,
	}
	if runtime.GOOS != "windows" { // backslash-escaped spaces are a Unix-terminal convention
		cases[strings.ReplaceAll(spaced, " ", `\ `)+" "+png] = 2
	}
	for in, want := range cases {
		if got := imagePathsIn(in); len(got) != want {
			t.Errorf("imagePathsIn(%q) = %v, want %d paths", in, got, want)
		}
	}
}

func TestTimelineMergesStreamingAndToolUpdates(t *testing.T) {
	tl := NewTimeline()
	items := []string{
		`{"type":"user_message","text":"hi"}`,
		`{"type":"assistant_message","text":"Hel","messageId":"m1"}`,
		`{"type":"assistant_message","text":"lo","messageId":"m1"}`,
		`{"type":"tool_call","callId":"c1","name":"bash","status":"running","detail":{"type":"shell","command":"ls"}}`,
		`{"type":"tool_call","callId":"c1","name":"bash","status":"completed","detail":{"type":"shell","command":"ls","output":"a\nb"}}`,
		`{"type":"todo","items":[{"text":"one","completed":false}]}`,
		`{"type":"todo","items":[{"text":"one","completed":true}]}`,
	}
	for _, it := range items {
		tl.Apply(json.RawMessage(it))
	}
	if len(tl.Entries) != 4 {
		t.Fatalf("want 4 entries (user, assistant, tool, todo), got %d", len(tl.Entries))
	}
	if got := tl.Entries[1].Text; got != "Hello" {
		t.Errorf("streamed fragments should merge, got %q", got)
	}
	if tool := tl.Entries[2].Tool; tool.Status != "completed" || tool.Detail.Output != "a\nb" {
		t.Errorf("tool call should update in place, got %+v", tool)
	}
	if !tl.Entries[3].Todos[0].Completed {
		t.Error("todo list should be replaced in place")
	}
}

func TestRewrapKeepsWidthAndHangingIndent(t *testing.T) {
	long := "  • " + strings.Repeat("word ", 30)
	out := rewrap(long, 40)
	for _, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("line wider than 40: %d %q", w, l)
		}
	}
	lines := strings.Split(out, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "    ") {
		t.Errorf("continuation lines should hang under the bullet text: %q", out)
	}
}

func TestAgentBucket(t *testing.T) {
	reason := func(s string) *string { return &s }
	cases := []struct {
		s    Snapshot
		want bucket
	}{
		{Snapshot{Status: "idle"}, bDone},
		{Snapshot{Status: "running"}, bRunning},
		{Snapshot{Status: "idle", RequiresAttention: true}, bAttention},
		{Snapshot{Status: "idle", RequiresAttention: true, AttentionReason: reason("error")}, bFailed},
		{Snapshot{Status: "running", PendingPermissions: []PermissionReq{{ID: "p"}}}, bNeedsInput},
	}
	for _, c := range cases {
		if got := agentBucket(c.s); got != c.want {
			t.Errorf("agentBucket(%+v) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestPromptHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	h := loadHistory()
	h.add("first")
	h.add("second")
	if s, _ := h.older("draft", []string{"from agent"}); s != "from agent" {
		t.Errorf("the agent's own prompts come first, got %q", s)
	}
	if s, _ := h.older("", nil); s != "second" {
		t.Errorf("then sent history, newest first, got %q", s)
	}
	h.newer()
	if s, _ := h.newer(); s != "draft" {
		t.Errorf("walking past the newest restores the draft, got %q", s)
	}
}

func TestFlexTime(t *testing.T) {
	var v struct{ A, B, C flexTime }
	if err := json.Unmarshal([]byte(`{"A":"2026-01-02T03:04:05Z","B":1700000000000,"C":null}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.Year() != 2026 || v.B.Year() != 2023 || !v.C.IsZero() {
		t.Errorf("unexpected times: %v %v %v", v.A, v.B, v.C)
	}
}

func TestPanelWidthsAreClamped(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := NewModel(BridgeConfig{HostLabel: "test"})
	m.width, m.height = 120, 40
	m.setSidebarWidth(5)
	if m.sideWidth() != minSidebarWidth {
		t.Errorf("sidebar should not go below %d, got %d", minSidebarWidth, m.sideWidth())
	}
	m.setSidebarWidth(500)
	if m.mainW() < minMainWidth {
		t.Errorf("the transcript should keep at least %d columns, got %d", minMainWidth, m.mainW())
	}
}

// fileURI is how terminals paste a dropped file: file:///abs/path on every OS.
func fileURI(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x -> /C:/x
	}
	return "file://" + p
}

func TestToolErrorText(t *testing.T) {
	cases := map[string]string{
		`"boom"`:                     "boom",
		`{"message":"no such file"}`: "no such file",
		`{"type":"tool_result","content":"Exit code 1\ntotal 12"}`: "Exit code 1\ntotal 12",
		`{"content":[{"type":"text","text":"denied"}]}`:            "denied",
	}
	for in, want := range cases {
		if got := toolErrorText(json.RawMessage(in)); got != want {
			t.Errorf("toolErrorText(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestForkedPromptCollapsesHistory(t *testing.T) {
	text := "<chat-history-summary>\nChat history from a previous Paseo agent.\nSource agent: Fix the login flow\nSource directory: /src\n\n[User] hi\n</chat-history-summary>\n\nNow add tests."
	raw, _ := json.Marshal(map[string]string{"type": "user_message", "text": text})
	tl := NewTimeline()
	tl.Apply(raw)
	e := tl.Entries[0]
	if !e.Forked || e.ForkedOf != "Fix the login flow" || e.Text != "Now add tests." {
		t.Fatalf("got forked=%v of=%q text=%q", e.Forked, e.ForkedOf, e.Text)
	}
	plain, _ := json.Marshal(map[string]string{"type": "user_message", "text": "mentions <chat-history-summary> mid-text"})
	tl.Apply(plain)
	if e := tl.Entries[1]; e.Forked || e.Text != "mentions <chat-history-summary> mid-text" {
		t.Fatalf("plain prompt changed: %+v", e)
	}
}

// The subagent list puts the newest on top, children under their parent, and
// mixes Paseo child agents and provider subagents by when they started.
func TestSubagentsNewestFirst(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	at := func(min int) flexTime { return flexTime{t0.Add(time.Duration(min) * time.Minute)} }
	parent := "p1"
	old := &ProviderSub{ID: "old", CreatedAt: at(1)}
	child := &ProviderSub{ID: "child", ParentSubagent: &old.ID, CreatedAt: at(5)}
	recent := &ProviderSub{ID: "recent", CreatedAt: at(3)}
	m := Model{
		agents: []Agent{{ID: "agent", CreatedAt: t0.Add(2 * time.Minute), Snap: Snapshot{Labels: map[string]string{parentLabel: parent}}}},
		subs:   map[string]map[string]*ProviderSub{parent: {"old": old, "child": child, "recent": recent}},
	}
	var got []string
	for _, e := range m.subagentsOf(parent) {
		id := ""
		if e.agent != nil {
			id = e.agent.ID
		} else {
			id = e.sub.ID
		}
		got = append(got, strings.Repeat(">", e.depth)+id)
	}
	want := "recent agent old >child"
	if strings.Join(got, " ") != want {
		t.Fatalf("order %q, want %q", strings.Join(got, " "), want)
	}
}

// A table wider than the transcript is laid out to fit, cells wrapped inside
// their columns, instead of each row being broken across lines.
func TestWideTableFits(t *testing.T) {
	r := NewRenderer(true)
	r.SetWidth(60)
	text := "Keys:\n\n| Key | Action | Notes |\n|---|---|---|\n" +
		"| `f` | Fork the agent into a new tab or a new workspace with the same provider, model and mode | starts from its chat history |\n" +
		"| `R` | Rename | short |\n"
	out := ansi.Strip(r.renderEntry(&Entry{Kind: "assistant", Text: text}))
	rows, widest := 0, 0
	for _, line := range strings.Split(out, "\n") {
		w := ansi.StringWidth(strings.TrimRight(line, " "))
		if w > 59 {
			t.Errorf("line %d wide: %q", w, line)
		}
		widest = max(widest, w)
		if strings.Count(line, "│") == 2 { // every table line keeps both column separators
			rows++
		}
	}
	if rows < 4 {
		t.Errorf("table rows lost their columns:\n%s", out)
	}
	if widest < 58 { // a table that has to wrap uses the whole line
		t.Errorf("table only %d of 59 columns wide:\n%s", widest, out)
	}
	// at every width, no table line is wider than the transcript (it would be wrapped mid-row)
	plan := "| Step | Change | File |\n|---|---|---|\n| 1 | `applyDiscountCode(cart, code)` parses `SAVE<n>` | `src/cart.js` |\n" +
		"| 3 | Tests for valid, invalid and missing codes | `test/cart.test.js` |\n"
	for w := 40; w <= 200; w++ {
		r.SetWidth(w)
		if _, ok := r.renderFit(plan); !ok {
			t.Errorf("width %d: the table doesn't fit", w)
		}
	}
}

// The settings file round-trips, missing keys keep their default, numbers and
// choices out of range are put back, and an unknown key is reported.
func TestConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	c := defaultConfig()
	c.Header.Diff, c.Startup.Open, c.Transcript.WheelLines, c.Tools.Editor = false, "command-center", 5, `emacs -nw "x"`
	if err := saveConfig(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := loadConfig(path)
	if err != nil || *got != *c {
		t.Fatalf("round trip: %v\n got %+v\nwant %+v", err, got, c)
	}
	if err := os.WriteFile(path, []byte("[header]\nshow = false\n[transcript]\nwheel_lines = 999\n[startup]\nopen = \"nope\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = loadConfig(path)
	if err != nil || got.Header.Show || !got.Header.Diff || got.Transcript.WheelLines != 20 || got.Startup.Open != "recent" {
		t.Fatalf("partial file: %v %+v", err, got)
	}
	_ = os.WriteFile(path, []byte("[header]\nshwo = true\n"), 0o644)
	if _, err := loadConfig(path); err == nil || !strings.Contains(err.Error(), "unknown setting header.shwo") {
		t.Fatalf("unknown key not reported: %v", err)
	}
	_ = os.WriteFile(path, []byte("[header\n"), 0o644)
	if _, err := loadConfig(path); err == nil {
		t.Fatal("broken file not reported")
	}
}

// 0.9.0 kept the hints switch under [footer]; such a file still loads, into
// general.keyboard_hints, and is written back under [general] only.
func TestConfigFooterHintsMoved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	_ = os.WriteFile(path, []byte("[footer]\nhints = false\n"), 0o644)
	c, err := loadConfig(path)
	if err != nil || c.General.KeyboardHints {
		t.Fatalf("old [footer] hints not read: %v %+v", err, c.General)
	}
	if err := saveConfig(path, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "[footer]") || !strings.Contains(string(b), "keyboard_hints = false") {
		t.Fatalf("written back wrong:\n%s", b)
	}
}

// An edit's diff keeps each line's own number and code: removed lines count
// on the old side, added and unchanged ones on the new side, past a hunk break.
func TestDiffBlockLines(t *testing.T) {
	s := newCodeStyles(true)
	diff := "--- a/x.go\n+++ b/x.go\n@@ -10,3 +10,3 @@\n func a() {\n-\treturn 1\n+\treturn 2\n }\n@@ -40,2 +40,3 @@\n x := \"a\n+y\"\n z()\n"
	got := strings.Split(ansi.Strip(s.diffBlock(diff, "", "", "x.go", 30, 60)), "\n")
	want := []string{"10   func a() {", "11 -     return 1", "11 +     return 2", "12   }", " ⋯", "40   x := \"a", "41 + y\"", "42   z()"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines:\n%s", len(got), strings.Join(got, "\n"))
	}
	for i := range want {
		if strings.TrimRight(got[i], " ") != want[i] {
			t.Errorf("line %d: %q, want %q", i, got[i], want[i])
		}
	}
	// without a diff, the old and new text are shown as removed and added
	got = strings.Split(ansi.Strip(s.diffBlock("", "a\n", "b\n", "x.txt", 30, 20)), "\n")
	if len(got) != 2 || !strings.HasPrefix(got[0], "- a") || !strings.HasPrefix(got[1], "+ b") {
		t.Errorf("old/new: %q", got)
	}
}
