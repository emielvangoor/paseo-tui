package main

import (
	"encoding/json"
	"path"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Add project (N): register an existing folder, create a new one, or clone a
// GitHub repository, the same three ways the desktop offers. Paths live on the
// daemon's machine.

const (
	srcExisting = iota
	srcNewFolder
	srcClone
)

var sourceLabels = []string{"Existing folder", "New folder", "Clone from GitHub"}

type ProjectForm struct {
	source int
	field  int             // 0 = source, then the inputs of that source
	path   textinput.Model // existing folder
	parent textinput.Model // new folder / clone: parent directory
	name   textinput.Model // new folder name
	repo   textinput.Model // owner/repo or URL
	busy   bool
	err    string
}

type projectAddedMsg struct {
	project EmptyProject
	err     error
}

func pfInput(placeholder, value string) textinput.Model {
	t := textinput.New()
	t.Placeholder = placeholder
	t.SetValue(value)
	t.Prompt = ""
	t.CharLimit = 0
	return t
}

func (m *Model) openProjectForm() (tea.Model, tea.Cmd) {
	if m.br == nil || !m.connected {
		m.setStatus("Not connected to the daemon", true)
		return *m, nil
	}
	// default parent: where the current project lives (paths are on the daemon's machine)
	parent := ""
	if v := m.wsViews[m.selWS]; v != nil {
		root := v.ws.ProjectRootPath
		if root == "" {
			root = v.ws.WorkspaceDirectory
		}
		if root != "" {
			parent = path.Dir(strings.TrimRight(root, "/"))
		}
	}
	f := &ProjectForm{
		path:   pfInput("/path/to/your/project", strings.TrimRight(parent, "/")+"/"),
		parent: pfInput("parent directory", parent),
		name:   pfInput("new-project", ""),
		repo:   pfInput("owner/repo or https://github.com/owner/repo", ""),
	}
	m.projForm = f
	m.overlay = ovProject
	return *m, nil
}

// inputs lists the text fields of the current source, in order.
func (f *ProjectForm) inputs() []*textinput.Model {
	switch f.source {
	case srcNewFolder:
		return []*textinput.Model{&f.parent, &f.name}
	case srcClone:
		return []*textinput.Model{&f.repo, &f.parent}
	}
	return []*textinput.Model{&f.path}
}

func (f *ProjectForm) focus() tea.Cmd {
	for _, in := range []*textinput.Model{&f.path, &f.parent, &f.name, &f.repo} {
		in.Blur()
	}
	if f.field > 0 {
		in := f.inputs()[f.field-1]
		in.CursorEnd()
		return in.Focus()
	}
	return nil
}

func (m *Model) projectFormKey(k tea.KeyMsg) tea.Cmd {
	f := m.projForm
	if f.busy {
		return nil
	}
	n := 1 + len(f.inputs())
	switch k.String() {
	case "esc":
		m.overlay, m.projForm = ovNone, nil
		if f := m.pendingNewForm; f != nil { // back to the New workspace form
			m.pendingNewForm = nil
			m.form, m.overlay = f, ovNew
		}
		return nil
	case "tab", "down":
		f.field = (f.field + 1) % n
		return f.focus()
	case "shift+tab", "up":
		f.field = (f.field - 1 + n) % n
		return f.focus()
	case "enter":
		return m.submitProject()
	}
	if f.field == 0 {
		switch k.String() {
		case "left", "h":
			f.source = (f.source + len(sourceLabels) - 1) % len(sourceLabels)
		case "right", "l", " ":
			f.source = (f.source + 1) % len(sourceLabels)
		}
		f.err = ""
		return nil
	}
	in := f.inputs()[f.field-1]
	var c tea.Cmd
	*in, c = in.Update(k)
	return c
}

func (m *Model) submitProject() tea.Cmd {
	f := m.projForm
	val := func(t textinput.Model) string { return strings.TrimSpace(t.Value()) }
	op, fields, what := "", map[string]any{}, ""
	switch f.source {
	case srcExisting:
		if val(f.path) == "" || strings.HasSuffix(val(f.path), "/") && len(val(f.path)) <= 1 {
			f.err = "Enter the folder's path."
			return nil
		}
		op, what = "addProject", "Adding project…"
		fields["path"] = strings.TrimRight(val(f.path), "/")
	case srcNewFolder:
		if val(f.parent) == "" || val(f.name) == "" {
			f.err = "Enter a parent directory and a name."
			return nil
		}
		op, what = "createProjectDirectory", "Creating folder…"
		fields["parentPath"], fields["name"] = val(f.parent), val(f.name)
	case srcClone:
		if val(f.repo) == "" || val(f.parent) == "" {
			f.err = "Enter a repository and a parent directory."
			return nil
		}
		op, what = "cloneProject", "Cloning…"
		fields["repo"], fields["parentPath"] = val(f.repo), val(f.parent)
	}
	f.busy, f.err = true, ""
	m.setStatus(what, false)
	br := m.br
	return func() tea.Msg {
		data, err := br.Call(op, fields, 10*time.Minute)
		var p EmptyProject
		if err == nil {
			err = json.Unmarshal(data, &p)
		}
		return projectAddedMsg{project: p, err: err}
	}
}

func (m Model) projectFormView() string {
	f := m.projForm
	w := max(60, min(90, m.width-8))
	inner := w - 4
	label := func(i int, s string) string {
		if f.field == i {
			return sAccent.Render("▸ ") + sAccent.Render(lipgloss.NewStyle().Width(11).Render(s))
		}
		return "  " + sMuted.Render(lipgloss.NewStyle().Width(11).Render(s))
	}
	var src []string
	for i, l := range sourceLabels {
		if i == f.source {
			src = append(src, sAccent.Render("‹ ")+sBold.Render(l)+sAccent.Render(" ›"))
		} else {
			src = append(src, sMuted.Render("  "+l+"  "))
		}
	}
	lines := []string{sAccent.Render(" Add project"), "", label(0, "Source") + strings.Join(src, " ")}
	names := map[int][]string{
		srcExisting:  {"Path"},
		srcNewFolder: {"Parent", "Name"},
		srcClone:     {"Repository", "Into"},
	}[f.source]
	for i, in := range f.inputs() {
		in.Width = inner - 16
		lines = append(lines, label(i+1, names[i])+in.View())
	}
	hint := map[int]string{
		srcExisting:  "Registers the folder as a project; nothing on disk changes.",
		srcNewFolder: "Creates <parent>/<name> on the daemon's machine.",
		srcClone:     "Clones into <into>/<repo name> on the daemon's machine.",
	}[f.source]
	lines = append(lines, "", sMuted.Render("  "+hint))
	if f.busy {
		lines = append(lines, "  "+m.spin.View()+sMuted.Render(" working…"))
	}
	if f.err != "" {
		lines = append(lines, sRed.Render("  "+f.err))
	}
	lines = append(lines, "", sMuted.Render("  tab next field · ←/→ source · enter add · esc cancel"))
	return frame(lines, w)
}
