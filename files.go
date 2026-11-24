package main

import (
	"os"
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
)

// workspaceDir is the open workspace's directory, else the focused agent's.
func (m Model) workspaceDir() string {
	if v := m.wsViews[m.selWS]; v != nil && v.ws.WorkspaceDirectory != "" {
		return v.ws.WorkspaceDirectory
	}
	if a := m.current(); a != nil {
		return a.Cwd
	}
	return ""
}

// localWorkspaceDir is workspaceDir when it exists on this machine; otherwise
// it says why in the status line.
func (m *Model) localWorkspaceDir() (string, bool) {
	dir := m.workspaceDir()
	if dir == "" {
		m.setStatus("Open a workspace first", true)
		return "", false
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		m.setStatus(shortHome(dir)+" isn't on this machine", true)
		return "", false
	}
	return dir, true
}

// fileManager is the command that shows a directory in the system's file
// manager, and that app's name.
func fileManager() (cmd, name string) {
	switch runtime.GOOS {
	case "darwin":
		return "open", "Finder"
	case "windows":
		return "explorer", "Explorer"
	}
	return "xdg-open", "Files"
}

// openInFiles shows the workspace directory in Finder, Explorer or the desktop's
// file manager; F falls back to it when no terminal file manager is installed.
func (m Model) openInFiles() (tea.Model, tea.Cmd) {
	dir, ok := m.localWorkspaceDir()
	if !ok {
		return m, nil
	}
	bin, name := fileManager()
	cmd := exec.Command(bin, dir)
	if err := cmd.Start(); err != nil {
		m.setStatus("Can't open "+name+": "+err.Error(), true)
		return m, nil
	}
	go func() { _ = cmd.Wait() }() // explorer exits 1 even when it worked, so the result isn't checked
	m.setStatus("Opened "+shortHome(dir)+" in "+name, false)
	return m, nil
}
