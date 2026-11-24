package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true}

type clipboardImageMsg struct {
	path string
	err  error
	none bool // clipboard holds no image: fall back to a text paste
}

func pasteDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "paseo-tui", "pastes")
}

// readClipboardImage saves an image from the clipboard (Wayland, then X11) to a file.
func readClipboardImage() tea.Msg {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var mime string
	var data []byte
	if _, err := exec.LookPath("wl-paste"); err == nil && os.Getenv("WAYLAND_DISPLAY") != "" {
		types, err := exec.CommandContext(ctx, "wl-paste", "--list-types").Output()
		if err != nil {
			return clipboardImageMsg{none: true}
		}
		mime = pickImageType(strings.Fields(string(types)))
		if mime == "" {
			return clipboardImageMsg{none: true}
		}
		data, err = exec.CommandContext(ctx, "wl-paste", "--no-newline", "--type", mime).Output()
		if err != nil {
			return clipboardImageMsg{err: fmt.Errorf("wl-paste: %w", err)}
		}
	} else if _, err := exec.LookPath("xclip"); err == nil {
		types, err := exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", "TARGETS", "-o").Output()
		if err != nil {
			return clipboardImageMsg{none: true}
		}
		mime = pickImageType(strings.Fields(string(types)))
		if mime == "" {
			return clipboardImageMsg{none: true}
		}
		data, err = exec.CommandContext(ctx, "xclip", "-selection", "clipboard", "-t", mime, "-o").Output()
		if err != nil {
			return clipboardImageMsg{err: fmt.Errorf("xclip: %w", err)}
		}
	} else if runtime.GOOS == "darwin" {
		return macClipboardImage(ctx)
	} else if runtime.GOOS == "windows" {
		return windowsClipboardImage(ctx)
	} else {
		return clipboardImageMsg{err: errors.New("install wl-clipboard (or xclip) to paste images")}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return clipboardImageMsg{none: true}
	}
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}[mime]
	if err := os.MkdirAll(pasteDir(), 0o755); err != nil {
		return clipboardImageMsg{err: err}
	}
	path := filepath.Join(pasteDir(), "paste-"+time.Now().Format("20060102-150405.000")+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return clipboardImageMsg{err: err}
	}
	return clipboardImageMsg{path: path}
}

func pickImageType(types []string) string {
	for _, want := range []string{"image/png", "image/jpeg", "image/webp", "image/gif"} {
		for _, t := range types {
			if t == want {
				return want
			}
		}
	}
	return ""
}

// imagePathsIn returns the image files in pasted text (paths or file:// URIs,
// as terminals produce when files are dropped onto them). It only matches when
// the whole paste is image paths, so normal text is never swallowed.
func imagePathsIn(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 8192 {
		return nil
	}
	var out []string
	for _, field := range splitPaths(text) {
		p := field
		if strings.HasPrefix(p, "file://") {
			if u, err := url.Parse(p); err == nil {
				p = u.Path
				// file:///C:/x parses to /C:/x on Windows
				if runtime.GOOS == "windows" && len(p) > 2 && p[0] == '/' && p[2] == ':' {
					p = filepath.FromSlash(p[1:])
				}
			}
		}
		p = expandHome(p)
		if !imageExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return nil
		}
		out = append(out, p)
	}
	return out
}

// splitPaths splits on newlines and unescaped spaces, honouring quotes and
// backslash-escaped spaces (as terminals emit for dropped files).
func splitPaths(s string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	esc := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && quote != '\'' && runtime.GOOS != "windows": // Windows paths use \ as a separator
			esc = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\n' || r == '\t' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

func (m *Model) attachHeight() int {
	if len(m.attachments) > 0 {
		return 1
	}
	return 0
}

func (m Model) viewAttachments(w int) string {
	if len(m.attachments) == 0 {
		return ""
	}
	chip := lipgloss.NewStyle().Foreground(cAccent).Background(cSelBg).Padding(0, 1)
	var parts []string
	for _, p := range m.attachments {
		size := ""
		if st, err := os.Stat(p); err == nil {
			size = " " + humanBytes(st.Size())
		}
		parts = append(parts, chip.Render("▣ "+truncate(filepath.Base(p), 28)+size))
	}
	line := " " + strings.Join(parts, " ") + sMuted.Render("  backspace removes")
	return truncate(line, w)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%dKB", n>>10)
	}
	return fmt.Sprintf("%dB", n)
}

// macClipboardImage writes the clipboard's image (as PNG) to a file via AppleScript.
func macClipboardImage(ctx context.Context) tea.Msg {
	if err := os.MkdirAll(pasteDir(), 0o755); err != nil {
		return clipboardImageMsg{err: err}
	}
	path := filepath.Join(pasteDir(), "paste-"+time.Now().Format("20060102-150405.000")+".png")
	script := []string{
		"-e", "try",
		"-e", "set img to (the clipboard as «class PNGf»)",
		"-e", "on error",
		"-e", "return \"none\"",
		"-e", "end try",
		"-e", fmt.Sprintf("set f to open for access POSIX file %q with write permission", path),
		"-e", "write img to f",
		"-e", "close access f",
		"-e", "return \"ok\"",
	}
	out, err := exec.CommandContext(ctx, "osascript", script...).Output()
	if err != nil {
		return clipboardImageMsg{err: fmt.Errorf("osascript: %w", err)}
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return clipboardImageMsg{none: true}
	}
	return clipboardImageMsg{path: path}
}

// windowsClipboardImage saves the clipboard's image as PNG via PowerShell.
func windowsClipboardImage(ctx context.Context) tea.Msg {
	if err := os.MkdirAll(pasteDir(), 0o755); err != nil {
		return clipboardImageMsg{err: err}
	}
	path := filepath.Join(pasteDir(), "paste-"+time.Now().Format("20060102-150405.000")+".png")
	script := "Add-Type -AssemblyName System.Windows.Forms; " +
		"$img = [System.Windows.Forms.Clipboard]::GetImage(); " +
		"if ($img -eq $null) { 'none' } else { $img.Save('" + strings.ReplaceAll(path, "'", "''") + "', [System.Drawing.Imaging.ImageFormat]::Png); 'ok' }"
	out, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-STA", "-Command", script).Output()
	if err != nil {
		return clipboardImageMsg{err: fmt.Errorf("powershell: %w", err)}
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return clipboardImageMsg{none: true}
	}
	return clipboardImageMsg{path: path}
}
