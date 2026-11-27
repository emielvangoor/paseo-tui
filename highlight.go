package main

// Syntax highlighting for the code in tool calls: edit diffs and the content of
// files read or written. Markdown code blocks are highlighted by glamour.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	cDiffAddBg = lipgloss.AdaptiveColor{Light: "#e3f6e8", Dark: "#1b2f23"}
	cDiffDelBg = lipgloss.AdaptiveColor{Light: "#fbe6e8", Dark: "#36191e"}
)

// a token of highlighted code
type codeTok struct {
	text  string
	style lipgloss.Style
}

// codeStyles turns chroma's token types into lipgloss styles, one map per theme.
type codeStyles struct {
	chroma *chroma.Style
	cache  map[chroma.TokenType]lipgloss.Style
}

func newCodeStyles(dark bool) *codeStyles {
	name := "catppuccin-latte"
	if dark {
		name = "catppuccin-mocha"
	}
	return &codeStyles{chroma: styles.Get(name), cache: map[chroma.TokenType]lipgloss.Style{}}
}

func (s *codeStyles) get(t chroma.TokenType) lipgloss.Style {
	if st, ok := s.cache[t]; ok {
		return st
	}
	e := s.chroma.Get(t)
	st := lipgloss.NewStyle()
	if e.Colour.IsSet() {
		st = st.Foreground(lipgloss.Color(e.Colour.String()))
	}
	st = st.Bold(e.Bold == chroma.Yes).Italic(e.Italic == chroma.Yes)
	s.cache[t] = st
	return st
}

// highlight splits code into lines of styled tokens, using the lexer for the
// file's name (or a guess from the code). The lines match strings.Split(code, "\n").
func (s *codeStyles) highlight(code, path string) [][]codeTok {
	lexer := lexers.Match(filepath.Base(path))
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lines := [][]codeTok{nil}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		for i, l := range strings.Split(code, "\n") {
			if i > 0 {
				lines = append(lines, nil)
			}
			lines[i] = []codeTok{{text: l}}
		}
		return lines
	}
	for tok := it(); tok != chroma.EOF; tok = it() {
		st := s.get(tok.Type)
		for i, part := range strings.Split(tok.Value, "\n") {
			if i > 0 {
				lines = append(lines, nil)
			}
			if part != "" {
				lines[len(lines)-1] = append(lines[len(lines)-1], codeTok{text: part, style: st})
			}
		}
	}
	// lexers end the code with a newline of their own
	if want := strings.Count(code, "\n") + 1; len(lines) > want {
		lines = lines[:want]
	}
	return lines
}

// paint renders a line of tokens, with bg behind them and padded with it to
// width when bg is set.
func paint(toks []codeTok, bg lipgloss.TerminalColor, width int) string {
	var b strings.Builder
	w := 0
	for _, t := range toks {
		st := t.style
		if bg != nil {
			st = st.Background(bg)
		}
		b.WriteString(st.Render(t.text))
		w += ansi.StringWidth(t.text)
	}
	if bg != nil && width > w {
		b.WriteString(lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", width-w)))
	}
	return b.String()
}

// codeBlock highlights the first n lines of a file's content, with line numbers.
func (s *codeStyles) codeBlock(content, path string, n int) string {
	content = strings.ReplaceAll(strings.TrimRight(content, "\n"), "\t", "    ")
	all := strings.Split(content, "\n")
	shown := all[:min(n, len(all))]
	hl := s.highlight(strings.Join(shown, "\n"), path)
	numW := len(strconv.Itoa(len(shown)))
	out := make([]string, 0, len(shown)+1)
	for i, toks := range hl {
		out = append(out, sMuted.Foreground(cBorder).Render(fmt.Sprintf("%*d ", numW, i+1))+paint(toks, nil, 0))
	}
	if len(all) > n {
		out = append(out, sMuted.Render(fmt.Sprintf("… %d more lines", len(all)-n)))
	}
	return strings.Join(out, "\n")
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// diffBlock renders the first n lines of a unified diff: line numbers, + and -
// markers, a tinted background on changed lines, and the code highlighted for
// the file's language. Without a diff, oldS and newS are shown as one.
func (s *codeStyles) diffBlock(diff, oldS, newS, path string, n, width int) string {
	if strings.TrimSpace(diff) == "" {
		var b strings.Builder
		for _, l := range strings.Split(strings.TrimRight(oldS, "\n"), "\n") {
			if oldS != "" {
				b.WriteString("-" + l + "\n")
			}
		}
		for _, l := range strings.Split(strings.TrimRight(newS, "\n"), "\n") {
			if newS != "" {
				b.WriteString("+" + l + "\n")
			}
		}
		diff = b.String()
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ") || strings.HasPrefix(l, "diff ") || strings.HasPrefix(l, "index ") {
			continue // the file is on the tool's line already
		}
		lines = append(lines, strings.ReplaceAll(l, "\t", "    "))
	}
	more := 0
	if len(lines) > n {
		lines, more = lines[:n], len(lines)-n
	}

	// each side is highlighted as a whole, so strings and comments that span
	// lines keep their colour
	var oldSide, newSide []string
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "@@"), strings.HasPrefix(l, `\`):
		case strings.HasPrefix(l, "-"):
			oldSide = append(oldSide, l[1:])
		case strings.HasPrefix(l, "+"):
			newSide = append(newSide, l[1:])
		default:
			c := strings.TrimPrefix(l, " ")
			oldSide, newSide = append(oldSide, c), append(newSide, c)
		}
	}
	oldHL := s.highlight(strings.Join(oldSide, "\n"), path)
	newHL := s.highlight(strings.Join(newSide, "\n"), path)

	// line numbers, as wide as the largest one shown
	maxN := 0
	for _, l := range lines {
		if m := hunkHeader.FindStringSubmatch(l); m != nil {
			a, _ := strconv.Atoi(m[1])
			c, _ := strconv.Atoi(m[2])
			maxN = max(maxN, a+len(lines), c+len(lines))
		}
	}
	numW := 0
	if maxN > 0 {
		numW = len(strconv.Itoa(maxN))
	}
	num := func(n int) string {
		if numW == 0 {
			return ""
		}
		if n <= 0 {
			return strings.Repeat(" ", numW+1)
		}
		return sMuted.Foreground(cBorder).Render(fmt.Sprintf("%*d ", numW, n))
	}
	codeW := width - 2
	if numW > 0 {
		codeW -= numW + 1
	}

	var out []string
	oi, ni, oldN, newN := 0, 0, 0, 0
	tok := func(hl [][]codeTok, i int) []codeTok {
		if i < len(hl) {
			return hl[i]
		}
		return nil
	}
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "@@"):
			if m := hunkHeader.FindStringSubmatch(l); m != nil {
				oldN, _ = strconv.Atoi(m[1])
				newN, _ = strconv.Atoi(m[2])
			}
			if i > 0 {
				out = append(out, sMuted.Foreground(cBorder).Render(strings.Repeat(" ", max(0, numW-1))+"⋯"))
			}
		case strings.HasPrefix(l, `\`):
			out = append(out, sMuted.Render(l))
		case strings.HasPrefix(l, "-"):
			out = append(out, num(oldN)+sRed.Background(cDiffDelBg).Render("- ")+paint(tok(oldHL, oi), cDiffDelBg, codeW))
			oi++
			if oldN > 0 {
				oldN++
			}
		case strings.HasPrefix(l, "+"):
			out = append(out, num(newN)+sGreen.Background(cDiffAddBg).Render("+ ")+paint(tok(newHL, ni), cDiffAddBg, codeW))
			ni++
			if newN > 0 {
				newN++
			}
		default:
			out = append(out, num(newN)+"  "+paint(tok(newHL, ni), nil, 0))
			oi, ni = oi+1, ni+1
			if oldN > 0 {
				oldN, newN = oldN+1, newN+1
			}
		}
	}
	if more > 0 {
		out = append(out, sMuted.Render(fmt.Sprintf("… %d more lines", more)))
	}
	return strings.Join(out, "\n")
}
