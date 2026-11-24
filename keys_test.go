package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
