package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	tea "github.com/charmbracelet/bubbletea"
)

// Settings live in <config dir>/paseo-tui/config.toml. The settings panel (c)
// writes it on every change; edits made to the file by hand are picked up while
// the app runs (it is checked every second).

type Config struct {
	General struct {
		KeyboardHints bool `toml:"keyboard_hints"`
	} `toml:"general"`
	Startup struct {
		Open string `toml:"open"` // recent | command-center | new
	} `toml:"startup"`
	Header struct {
		Show        bool `toml:"show"`
		Branch      bool `toml:"branch"`
		PullRequest bool `toml:"pull_request"`
		Diff        bool `toml:"diff"`
	} `toml:"header"`
	Tabs struct {
		Model bool `toml:"model"`
	} `toml:"tabs"`
	Sidebar struct {
		Numbers bool `toml:"numbers"`
	} `toml:"sidebar"`
	Transcript struct {
		Thinking   bool `toml:"thinking"`
		Verbose    bool `toml:"verbose"`
		WheelLines int  `toml:"wheel_lines"`
		WheelMax   int  `toml:"wheel_max"`
		KeyLines   int  `toml:"key_lines"`
	} `toml:"transcript"`
	// COMPAT: 0.9.0 had [footer] hints; it's read into general.keyboard_hints and not written back.
	Footer struct {
		Hints *bool `toml:"hints"`
	} `toml:"footer"`
	Behavior struct {
		ConfirmQuit   bool `toml:"confirm_quit"`
		TerminalTitle bool `toml:"terminal_title"`
		UpdateCheck   bool `toml:"update_check"`
	} `toml:"behavior"`
	Tools struct {
		Editor      string `toml:"editor"`
		FileManager string `toml:"file_manager"`
	} `toml:"tools"`
}

func defaultConfig() *Config {
	c := &Config{}
	c.Startup.Open = "recent"
	c.Header.Show, c.Header.Branch, c.Header.PullRequest, c.Header.Diff = true, true, true, true
	c.Tabs.Model = true
	c.Sidebar.Numbers = true
	c.Transcript.WheelLines, c.Transcript.WheelMax, c.Transcript.KeyLines = 2, 8, 3
	c.General.KeyboardHints = true
	c.Behavior.ConfirmQuit, c.Behavior.TerminalTitle, c.Behavior.UpdateCheck = true, true, true
	return c
}

// ---------- the settings, in order: the panel and the file follow this list ----------

type settingKind int

const (
	setBool settingKind = iota
	setInt
	setString
	setChoice
)

type setting struct {
	section, key, label, help string
	kind                      settingKind
	b                         *bool
	i                         *int
	s                         *string
	min, max                  int
	choices                   [][2]string // value, label
	hidden                    bool        // only matters when another setting is on (left out of the panel)
}

var sectionTitles = map[string]string{
	"general": "General", "startup": "Startup", "header": "Header", "tabs": "Tabs", "sidebar": "Sidebar",
	"transcript": "Transcript", "behavior": "Behavior", "tools": "Tools",
}

func (c *Config) settings() []setting {
	return []setting{
		{section: "general", key: "keyboard_hints", label: "Keyboard hints", kind: setBool, b: &c.General.KeyboardHints,
			help: "Shortcut hints everywhere: the bottom bar, + new on the tab bar, \"t to show\", \"u to open\", \"G to follow\". Off hides the bottom bar; messages show on the message box border."},
		{section: "startup", key: "open", label: "On start, open", kind: setChoice, s: &c.Startup.Open,
			choices: [][2]string{{"recent", "the most recent agent"}, {"command-center", "the command center"}, {"new", "a new workspace"}},
			help:    "What paseo-tui shows when it starts: the agent you used last, the command center to pick one, or the New workspace form (N)."},
		{section: "header", key: "show", label: "Show the header", kind: setBool, b: &c.Header.Show,
			help: "The project › workspace line above the tabs. Off gives the transcript one more line."},
		{section: "header", key: "branch", label: "Branch", kind: setBool, b: &c.Header.Branch, hidden: !c.Header.Show,
			help: "The workspace's git branch in the header."},
		{section: "header", key: "pull_request", label: "Pull request", kind: setBool, b: &c.Header.PullRequest, hidden: !c.Header.Show,
			help: "The workspace's pull request number in the header."},
		{section: "header", key: "diff", label: "Diff (+added −removed)", kind: setBool, b: &c.Header.Diff, hidden: !c.Header.Show,
			help: "The workspace's uncommitted changes in the header."},
		{section: "tabs", key: "model", label: "Model and thinking level", kind: setBool, b: &c.Tabs.Model,
			help: "The focused agent's model and thinking level at the right end of the tab bar."},
		{section: "sidebar", key: "numbers", label: "Number the workspaces", kind: setBool, b: &c.Sidebar.Numbers,
			help: "Number workspaces under their heading (1. main), in the sidebar and the header."},
		{section: "transcript", key: "thinking", label: "Show thinking", kind: setBool, b: &c.Transcript.Thinking,
			help: "Show the agents' thinking. t switches it for now."},
		{section: "transcript", key: "verbose", label: "Verbose tool output", kind: setBool, b: &c.Transcript.Verbose,
			help: "Show command output, diffs and file contents under tool calls. v switches it for now."},
		{section: "transcript", key: "wheel_lines", label: "Lines per wheel notch", kind: setInt, i: &c.Transcript.WheelLines, min: 1, max: 20,
			help: "How far one notch of the mouse wheel (or a trackpad step) scrolls."},
		{section: "transcript", key: "wheel_max", label: "Fastest wheel scrolling", kind: setInt, i: &c.Transcript.WheelMax, min: 1, max: 50,
			help: "The most lines one wheel step scrolls while you keep spinning."},
		{section: "transcript", key: "key_lines", label: "Lines per J / K", kind: setInt, i: &c.Transcript.KeyLines, min: 1, max: 30,
			help: "How far J and K scroll the transcript."},
		{section: "behavior", key: "confirm_quit", label: "Ask before quitting", kind: setBool, b: &c.Behavior.ConfirmQuit,
			help: "q and ctrl+c ask first. Open editor or file manager panes are always asked about."},
		{section: "behavior", key: "terminal_title", label: "Set the terminal title", kind: setBool, b: &c.Behavior.TerminalTitle,
			help: "The terminal or tab title follows the open workspace and agent."},
		{section: "behavior", key: "update_check", label: "Check for updates", kind: setBool, b: &c.Behavior.UpdateCheck,
			help: "Look for a new paseo-tui release at start (at most once a day)."},
		{section: "tools", key: "editor", label: "Editor (e)", kind: setString, s: &c.Tools.Editor,
			help: "Command for e, e.g. nvim or emacs -nw. Empty: $VISUAL, then $EDITOR, then nvim or vim."},
		{section: "tools", key: "file_manager", label: "File manager (F)", kind: setString, s: &c.Tools.FileManager,
			help: "Command for F, e.g. ranger or yazi. Empty: the first of ranger, yazi, lf and nnn."},
	}
}

// value is the setting's current value as shown in the panel.
func (s setting) value() string {
	switch s.kind {
	case setBool:
		return map[bool]string{true: "on", false: "off"}[*s.b]
	case setInt:
		return fmt.Sprint(*s.i)
	case setChoice:
		for _, c := range s.choices {
			if c[0] == *s.s {
				return c[1]
			}
		}
	}
	return *s.s
}

// ---------- the file ----------

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "paseo-tui", "config.toml")
}

// fileStamp tells whether the file changed since it was last read or written.
type fileStamp struct {
	mod  time.Time
	size int64
}

func stampOf(path string) fileStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{st.ModTime(), st.Size()}
}

// loadConfig reads the file over the defaults (keys it lacks keep their default),
// then puts out-of-range numbers and unknown choices back in range.
func loadConfig(path string) (*Config, error) {
	c := defaultConfig()
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	md, err := toml.Decode(string(b), c)
	if err != nil {
		return defaultConfig(), fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if h := c.Footer.Hints; h != nil {
		c.General.KeyboardHints, c.Footer.Hints = *h, nil
	}
	c.clamp()
	if u := md.Undecoded(); len(u) > 0 {
		return c, fmt.Errorf("%s: unknown setting %s", filepath.Base(path), u[0])
	}
	return c, nil
}

func (c *Config) clamp() {
	for _, s := range c.settings() {
		switch s.kind {
		case setInt:
			*s.i = min(max(*s.i, s.min), s.max)
		case setChoice:
			ok := false
			for _, ch := range s.choices {
				ok = ok || ch[0] == *s.s
			}
			if !ok {
				*s.s = s.choices[0][0]
			}
		}
	}
	if c.Transcript.WheelMax < c.Transcript.WheelLines {
		c.Transcript.WheelMax = c.Transcript.WheelLines
	}
}

// saveConfig writes the file in the settings' order, with each one's help as a comment.
func saveConfig(path string, c *Config) error {
	if path == "" {
		return errors.New("no config directory")
	}
	var b strings.Builder
	b.WriteString("# paseo-tui settings. Change them in the app (c) or here: saving this file applies them\n")
	b.WriteString("# while paseo-tui runs. The app rewrites this file when you change a setting there,\n")
	b.WriteString("# so comments you add are not kept.\n")
	section := ""
	for _, s := range c.settings() {
		if s.section != section {
			section = s.section
			fmt.Fprintf(&b, "\n[%s]\n", section)
		}
		fmt.Fprintf(&b, "\n# %s\n", s.help)
		switch s.kind {
		case setBool:
			fmt.Fprintf(&b, "%s = %t\n", s.key, *s.b)
		case setInt:
			fmt.Fprintf(&b, "# %d to %d\n%s = %d\n", s.min, s.max, s.key, *s.i)
		case setChoice:
			var vals []string
			for _, ch := range s.choices {
				vals = append(vals, fmt.Sprintf("%q (%s)", ch[0], ch[1]))
			}
			fmt.Fprintf(&b, "# %s\n%s = %s\n", strings.Join(vals, " or "), s.key, tomlString(*s.s))
		default:
			fmt.Fprintf(&b, "%s = %s\n", s.key, tomlString(*s.s))
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// tomlString is a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ---------- reloading ----------

type configTickMsg struct{}

func configTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return configTickMsg{} })
}

// checkConfigFile reloads the settings when the file changed on disk.
func (m *Model) checkConfigFile() {
	path := configPath()
	st := stampOf(path)
	if st == m.confStamp {
		return
	}
	m.confStamp = st
	c, err := loadConfig(path)
	if err != nil && c == nil {
		return
	}
	if err != nil && strings.Contains(err.Error(), "unknown setting") {
		m.setConfig(c)
		m.setStatus("Settings reloaded · "+err.Error(), true)
		return
	}
	if err != nil {
		m.setStatus("Settings not reloaded: "+err.Error(), true)
		return
	}
	m.setConfig(c)
	m.setStatus("Settings reloaded", false)
}

// setConfig switches to c and applies what changed.
func (m *Model) setConfig(c *Config) {
	old := m.prefs
	m.prefs = c
	if old == nil || old.Transcript.Thinking != c.Transcript.Thinking {
		m.render.ShowThk = c.Transcript.Thinking
	}
	if old == nil || old.Transcript.Verbose != c.Transcript.Verbose {
		m.render.Verbose = c.Transcript.Verbose
	}
	if m.render.Hints != c.General.KeyboardHints {
		m.render.Hints = c.General.KeyboardHints
		m.render.Reset() // cached "thinking… (N chars, t to show)" lines
	}
	m.rebuildRows()
	m.layout()
	m.refreshViewport()
}

// saveSettings writes the settings after a change in the panel.
func (m *Model) saveSettings() {
	path := configPath()
	if err := saveConfig(path, m.prefs); err != nil {
		m.setStatus("Settings not saved: "+err.Error(), true)
		return
	}
	m.confStamp = stampOf(path)
}
