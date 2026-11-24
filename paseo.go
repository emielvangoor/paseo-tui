package main

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Agent is the TUI's view of an agent, derived from a daemon Snapshot.
type Agent struct {
	ID          string
	ShortID     string
	Name        string
	Provider    string // provider[/model]
	Thinking    string
	Status      string
	Cwd         string
	Created     string
	WorkspaceID *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Snap        Snapshot
}

type Provider struct {
	Provider    string `json:"provider"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	Enabled     string `json:"enabled"`
	DefaultMode string `json:"defaultMode"`
}

type RunOptions struct {
	Prompt      string   `json:"prompt"`
	Title       string   `json:"title,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Model       string   `json:"model,omitempty"`
	Mode        string   `json:"mode,omitempty"`
	Cwd         string   `json:"cwd,omitempty"`
	Worktree    bool     `json:"worktree,omitempty"`
	NewLocal    bool     `json:"newLocal,omitempty"`
	WorkspaceID string   `json:"workspaceId,omitempty"`
	Images      []string `json:"images,omitempty"`
}

func (o RunOptions) fields() map[string]any {
	b, _ := json.Marshal(o)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			return h + p[1:]
		}
	}
	return p
}
