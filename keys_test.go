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
