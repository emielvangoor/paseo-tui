package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"
)

// Floating terminal panes: a program runs in a pseudo-terminal in the open
// workspace's directory and is drawn in a box over the UI, while agents keep
// updating behind it. e runs your editor, F a file manager. Every key goes to
// the program; ctrl+\ hides the pane (the program keeps running and the same key
// brings it back), and quitting the program closes it. Each workspace directory
// has its own panes, so switching workspaces never shows another one's.

type paneKind int

const (
	paneEditor paneKind = iota // e
	paneFiles                  // F
)

var paneKeys = [...]string{paneEditor: "e", paneFiles: "F"}

// paneID identifies a pane: one of each kind per workspace directory.
type paneID struct {
	kind paneKind
	dir  string
}

type termPane struct {
	kind  paneKind
	pty   xpty.Pty
	cmd   *exec.Cmd
	emu   *vt.SafeEmulator
	name  string // program, for the header
	dir   string
	w, h  int           // emulator size in cells
	dirty chan struct{} // the screen changed
	done  chan error    // the program exited
}

func (p *termPane) id() paneID { return paneID{p.kind, p.dir} }

type paneOutputMsg struct{ p *termPane }
type paneExitMsg struct {
	p   *termPane
	err error
}

// firstInstalled returns the first command whose program is on the PATH.
func firstInstalled(cmds ...string) []string {
	for _, c := range cmds {
		if argv := strings.Fields(c); len(argv) > 0 {
			if _, err := exec.LookPath(argv[0]); err == nil {
				return argv
			}
		}
	}
	return nil
}

// filesManagers are the terminal file managers F looks for, in order.
var filesManagers = []string{"ranger", "yazi", "lf", "nnn"}

// paneCommand is the program for a pane: the one set in the settings, else the
// first one installed.
func paneCommand(kind paneKind, prefs *Config) ([]string, error) {
	set := prefs.Tools.Editor
	if kind == paneFiles {
		set = prefs.Tools.FileManager
	}
	if strings.TrimSpace(set) != "" {
		if argv := firstInstalled(set); argv != nil {
			return argv, nil
		}
		return nil, fmt.Errorf("%s (from the settings) is not installed", strings.Fields(set)[0])
	}
	if kind == paneFiles {
		if argv := firstInstalled(filesManagers...); argv != nil {
			return argv, nil
		}
		return nil, errors.New("no terminal file manager found")
	}
	if argv := firstInstalled(os.Getenv("VISUAL"), os.Getenv("EDITOR"), "nvim", "vim", "vi"); argv != nil {
		return argv, nil
	}
	return nil, errors.New("no editor found: set $EDITOR")
}

func startPane(kind paneKind, argv []string, dir string, w, h int) (*termPane, error) {
	pty, err := xpty.NewPty(w, h)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(argv[0], append(argv[1:], ".")...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ownTerminal(cmd)
	if err := pty.Start(cmd); err != nil {
		_ = pty.Close()
		return nil, fmt.Errorf("%s: %w", argv[0], err)
	}
	p := &termPane{
		kind: kind, pty: pty, cmd: cmd, emu: vt.NewSafeEmulator(w, h),
		name: filepath.Base(argv[0]), dir: dir, w: w, h: h,
		dirty: make(chan struct{}, 1), done: make(chan error, 1),
	}
	go func() { // program output -> screen
		buf := make([]byte, 32*1024)
		for {
			n, err := pty.Read(buf)
			if n > 0 {
				_, _ = p.emu.Write(buf[:n])
				select {
				case p.dirty <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { // keys and terminal replies -> program
		buf := make([]byte, 4096)
		for {
			n, err := p.emu.Read(buf)
			if n > 0 {
				_, _ = pty.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		err := xpty.WaitProcess(context.Background(), cmd)
		_ = p.emu.Close()
		_ = pty.Close()
		p.done <- err
	}()
	return p, nil
}

func waitPane(p *termPane) tea.Cmd {
	return func() tea.Msg {
		select {
		case <-p.dirty:
			return paneOutputMsg{p}
		case err := <-p.done:
			return paneExitMsg{p, err}
		}
	}
}

func (p *termPane) resize(w, h int) {
	if w == p.w && h == p.h {
		return
	}
	p.w, p.h = w, h
	p.emu.Resize(w, h)
	_ = p.pty.Resize(w, h)
}

// kill ends the program; used when paseo-tui quits.
func (p *termPane) kill() {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// runningPanes lists the panes whose program is still running, shown or hidden,
// in a stable order: by directory, editor before files.
func (m Model) runningPanes() []*termPane {
	out := make([]*termPane, 0, len(m.panes))
	for _, p := range m.panes {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dir != out[j].dir {
			return out[i].dir < out[j].dir
		}
		return out[i].kind < out[j].kind
	})
	return out
}

// ---------- geometry ----------

// paneBox is the pane's outer size; the emulator gets it minus the border and header row.
func (m Model) paneBox() (w, h int) {
	w = min(m.width-2, max(40, m.width*92/100))
	h = min(m.height-2, max(10, m.height*90/100))
	return w, h
}

// paneOrigin is the screen position of the emulator's top-left cell.
func (m Model) paneOrigin() (x, y int) {
	w, h := m.paneBox()
	return max(0, (m.width-w)/2) + 1, max(0, (m.height-h)/2) + 2
}

// openPane shows this workspace's pane of this kind, starting its program in the
// workspace's directory when it isn't running yet.
func (m *Model) openPane(kind paneKind) (tea.Model, tea.Cmd) {
	argv, err := paneCommand(kind, m.prefs)
	if kind == paneFiles && err != nil && strings.TrimSpace(m.prefs.Tools.FileManager) == "" {
		// no terminal file manager: show the directory in Finder / Explorer / Files instead
		mm, c := m.openInFiles()
		mod := mm.(Model)
		if !mod.isErr {
			mod.setStatus(mod.status+" · install "+strings.Join(filesManagers, ", ")+" to browse in a pane", false)
		}
		return mod, c
	}
	dir, ok := m.localWorkspaceDir()
	if !ok {
		return *m, nil
	}
	w, h := m.paneBox()
	id := paneID{kind, dir}
	if p := m.panes[id]; p != nil { // still running: bring it back
		p.resize(w-2, h-3)
		m.overlay, m.paneShown = ovPane, id
		return *m, nil
	}
	if err != nil {
		m.setStatus(err.Error(), true)
		return *m, nil
	}
	p, err := startPane(kind, argv, dir, w-2, h-3)
	if err != nil {
		m.setStatus(err.Error(), true)
		return *m, nil
	}
	m.panes[id] = p
	m.overlay, m.paneShown = ovPane, id
	if m.focus == focusInput {
		m.focus = focusList
		m.input.Blur()
	}
	return *m, waitPane(p)
}

// ---------- input ----------

// paneKey sends a key to the shown pane's program; ctrl+\ hides the pane instead.
func (m *Model) paneKey(k tea.KeyMsg) {
	p := m.panes[m.paneShown]
	if k.Type == tea.KeyCtrlBackslash {
		m.overlay = ovNone
		m.setStatus(p.name+" hidden; "+paneKeys[p.kind]+" brings it back", false)
		return
	}
	p.sendKey(k)
}

// final bytes of the xterm sequences for modified cursor, editing and function keys
var paneKeyCSI = map[string]string{
	"up": "A", "down": "B", "right": "C", "left": "D", "home": "H", "end": "F",
	"f1": "P", "f2": "Q", "f3": "R", "f4": "S",
	"insert": "2~", "delete": "3~", "pgup": "5~", "pgdown": "6~",
	"f5": "15~", "f6": "17~", "f7": "18~", "f8": "19~", "f9": "20~", "f10": "21~", "f11": "23~", "f12": "24~",
}

var paneKeyCode = map[string]rune{
	"up": uv.KeyUp, "down": uv.KeyDown, "right": uv.KeyRight, "left": uv.KeyLeft,
	"home": uv.KeyHome, "end": uv.KeyEnd, "insert": uv.KeyInsert, "delete": uv.KeyDelete,
	"pgup": uv.KeyPgUp, "pgdown": uv.KeyPgDown,
	"f1": uv.KeyF1, "f2": uv.KeyF2, "f3": uv.KeyF3, "f4": uv.KeyF4, "f5": uv.KeyF5, "f6": uv.KeyF6,
	"f7": uv.KeyF7, "f8": uv.KeyF8, "f9": uv.KeyF9, "f10": uv.KeyF10, "f11": uv.KeyF11, "f12": uv.KeyF12,
}

// paneKeySeq is what a terminal sends for k. Unmodified cursor and function
// keys return ok=false with their key code, so the emulator encodes them for
// the program's current cursor-key mode.
func paneKeySeq(k tea.KeyMsg) (seq string, code rune, ok bool) {
	esc := ""
	if k.Alt {
		esc = "\x1b"
	}
	switch {
	case k.Type == tea.KeyRunes:
		return esc + string(k.Runes), 0, true
	case k.Type == tea.KeySpace:
		return esc + " ", 0, true
	case k.Type >= 0 && k.Type <= 31 || k.Type == 127: // control characters, enter, tab, esc, backspace
		return esc + string(rune(k.Type)), 0, true
	}
	name := strings.TrimPrefix(k.String(), "alt+")
	if name == "shift+tab" {
		return esc + "\x1b[Z", 0, true
	}
	ctrl := strings.HasPrefix(name, "ctrl+")
	shift := strings.Contains(name, "shift+")
	name = strings.TrimPrefix(strings.TrimPrefix(name, "ctrl+"), "shift+")
	if !ctrl && !shift {
		if c, ok := paneKeyCode[name]; ok {
			return "", c, false
		}
	}
	mod := 1 // xterm's modifier parameter: 1 + shift(1) + alt(2) + ctrl(4)
	if shift {
		mod++
	}
	if k.Alt {
		mod += 2
	}
	if ctrl {
		mod += 4
	}
	final, ok := paneKeyCSI[name]
	if !ok {
		return "", 0, true // nothing to send
	}
	if num, isTilde := strings.CutSuffix(final, "~"); isTilde {
		return fmt.Sprintf("\x1b[%s;%d~", num, mod), 0, true
	}
	return fmt.Sprintf("\x1b[1;%d%s", mod, final), 0, true
}

func (p *termPane) sendKey(k tea.KeyMsg) {
	if k.Paste {
		p.emu.Paste(string(k.Runes))
		return
	}
	seq, code, ok := paneKeySeq(k)
	if !ok {
		var mod uv.KeyMod
		if k.Alt {
			mod = uv.ModAlt
		}
		p.emu.SendKey(uv.KeyPressEvent{Code: code, Mod: mod})
		return
	}
	if seq != "" {
		p.emu.SendText(seq)
	}
}

// paneMouse forwards mouse events inside the shown pane (the emulator drops
// them unless the program asked for mouse reporting).
func (m *Model) paneMouse(msg tea.MouseMsg) {
	p := m.panes[m.paneShown]
	ox, oy := m.paneOrigin()
	x, y := msg.X-ox, msg.Y-oy
	if x < 0 || y < 0 || x >= p.w || y >= p.h {
		return
	}
	var mod uv.KeyMod
	if msg.Alt {
		mod |= uv.ModAlt
	}
	if msg.Ctrl {
		mod |= uv.ModCtrl
	}
	if msg.Shift {
		mod |= uv.ModShift
	}
	btn := map[tea.MouseButton]uv.MouseButton{
		tea.MouseButtonLeft: uv.MouseLeft, tea.MouseButtonMiddle: uv.MouseMiddle, tea.MouseButtonRight: uv.MouseRight,
		tea.MouseButtonWheelUp: uv.MouseWheelUp, tea.MouseButtonWheelDown: uv.MouseWheelDown,
		tea.MouseButtonWheelLeft: uv.MouseWheelLeft, tea.MouseButtonWheelRight: uv.MouseWheelRight,
	}[msg.Button]
	ev := uv.Mouse{X: x, Y: y, Button: btn, Mod: mod}
	switch {
	case tea.MouseEvent(msg).IsWheel():
		p.emu.SendMouse(uv.MouseWheelEvent(ev))
	case msg.Action == tea.MouseActionPress:
		p.emu.SendMouse(uv.MouseClickEvent(ev))
	case msg.Action == tea.MouseActionRelease:
		p.emu.SendMouse(uv.MouseReleaseEvent(ev))
	case msg.Action == tea.MouseActionMotion:
		p.emu.SendMouse(uv.MouseMotionEvent(ev))
	}
}

// ---------- view ----------

func (m Model) paneView() string {
	p := m.panes[m.paneShown]
	lines := strings.Split(p.emu.Render(), "\n")
	if len(lines) > p.h {
		lines = lines[:p.h]
	}
	for len(lines) < p.h {
		lines = append(lines, "")
	}
	if !p.emu.CursorHidden() {
		c := p.emu.CursorPosition()
		if c.Y >= 0 && c.Y < len(lines) && c.X >= 0 && c.X < p.w {
			line := lines[c.Y]
			under := ansi.Strip(ansi.Cut(line, c.X, c.X+1))
			if under == "" {
				under = " "
			}
			lines[c.Y] = ansi.Cut(line, 0, c.X) + "\x1b[0;7m" + under + "\x1b[0m" + ansi.Cut(line, c.X+1, p.w)
		}
	}
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, p.w, "") + "\x1b[0m" + strings.Repeat(" ", max(0, p.w-ansi.StringWidth(l)))
	}
	header := rightAlign(sAccent.Render(" "+p.name)+sMuted.Render(" · "+shortHome(p.dir)),
		sMuted.Render("ctrl+\\ hide · quit "+p.name+" to close "), p.w)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cAccent).
		Render(header + "\n" + strings.Join(lines, "\n"))
}
