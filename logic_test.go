package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
