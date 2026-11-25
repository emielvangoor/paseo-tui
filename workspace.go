package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Workspace mirrors the daemon's WorkspaceDescriptorPayload (subset).
type Workspace struct {
	ID                 string   `json:"id"`
	ProjectID          string   `json:"projectId"`
	ProjectDisplayName string   `json:"projectDisplayName"`
	ProjectCustomName  *string  `json:"projectCustomName"`
	ProjectRootPath    string   `json:"projectRootPath"`
	ProjectKind        string   `json:"projectKind"` // git | non_git | directory
	WorkspaceDirectory string   `json:"workspaceDirectory"`
	Name               string   `json:"name"`
	Title              *string  `json:"title"`
	WorkspaceKind      string   `json:"workspaceKind"`
	WorktreeSlug       *string  `json:"worktreeSlug"`
	Status             string   `json:"status"`
	StatusEnteredAt    flexTime `json:"statusEnteredAt"`
	ActivityAt         flexTime `json:"activityAt"`
	CreatedAt          flexTime `json:"createdAt"`
	LastUserMessageAt  flexTime `json:"lastUserMessageAt"`
	PinnedAt           flexTime `json:"pinnedAt"`
	Background         bool     `json:"background"`
	DiffStat           *struct {
		Additions int `json:"additions"`
		Deletions int `json:"deletions"`
	} `json:"diffStat"`
	GitRuntime *struct {
		CurrentBranch *string `json:"currentBranch"`
		IsDirty       bool    `json:"isDirty"`
	} `json:"gitRuntime"`
	GithubRuntime *struct {
		PullRequest *struct {
			Number   int    `json:"number"`
			State    string `json:"state"`
			IsDraft  bool   `json:"isDraft"`
			IsMerged bool   `json:"isMerged"`
		} `json:"pullRequest"`
	} `json:"githubRuntime"`

	synthetic bool // derived from agents because the daemon didn't send it
}

type EmptyProject struct {
	ProjectID          string  `json:"projectId"`
	ProjectDisplayName string  `json:"projectDisplayName"`
	ProjectCustomName  *string `json:"projectCustomName"`
	ProjectRootPath    string  `json:"projectRootPath"`
	ProjectKind        string  `json:"projectKind"`
}

// flexTime accepts an ISO string, a unix-ms number, or null.
type flexTime struct{ time.Time }

func (t *flexTime) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		if s == "" {
			return nil
		}
		if v, err := time.Parse(time.RFC3339Nano, s); err == nil {
			t.Time = v
		}
		return nil
	}
	var n float64
	if json.Unmarshal(b, &n) == nil && n > 0 {
		t.Time = time.UnixMilli(int64(n))
	}
	return nil
}

func (w *Workspace) ProjectName() string {
	if w.ProjectCustomName != nil && *w.ProjectCustomName != "" {
		return *w.ProjectCustomName
	}
	if w.ProjectDisplayName != "" {
		return w.ProjectDisplayName
	}
	return filepath.Base(w.ProjectRootPath)
}

func (w *Workspace) DisplayName() string {
	if w.Title != nil && *w.Title != "" {
		return *w.Title
	}
	if w.Name != "" {
		return w.Name
	}
	return filepath.Base(w.WorkspaceDirectory)
}

func (w *Workspace) Branch() string {
	if w.GitRuntime != nil && w.GitRuntime.CurrentBranch != nil {
		return *w.GitRuntime.CurrentBranch
	}
	return ""
}

func (w *Workspace) IsWorktree() bool { return w.WorkspaceKind == "worktree" }

// ---------- status buckets (same rules as the desktop app) ----------

type bucket int

const (
	bDone bucket = iota
	bAttention
	bRunning
	bFailed
	bNeedsInput
)

var bucketTitle = map[bucket]string{
	bNeedsInput: "Needs input",
	bFailed:     "Failed",
	bAttention:  "Ready to review",
	bRunning:    "Working",
	bDone:       "Done",
}

// statusOrder is the order of the sidebar's "group by status" sections.
var statusOrder = []bucket{bNeedsInput, bFailed, bAttention, bRunning, bDone}

func bucketFromString(s string) bucket {
	switch s {
	case "needs_input":
		return bNeedsInput
	case "failed":
		return bFailed
	case "running":
		return bRunning
	case "attention":
		return bAttention
	}
	return bDone
}

// agentBucket follows protocol/src/agent-state-bucket.ts.
func agentBucket(s Snapshot) bucket {
	reason := deref(s.AttentionReason)
	switch {
	case len(s.PendingPermissions) > 0 || reason == "permission":
		return bNeedsInput
	case s.RequiresAttention && reason == "error":
		return bFailed
	case s.Status == "running" || (s.BackgroundWork != nil && s.BackgroundWork.running() > 0):
		return bRunning
	case s.RequiresAttention:
		return bAttention
	}
	return bDone
}

// rootTabAgent: agents the desktop opens as tabs (root, not archived, not background).
func rootTabAgent(a Agent) bool {
	s := a.Snap
	// delegated (child) agents live in their parent's subagent track, not as tabs
	return s.ArchivedAt == nil && !s.Background && s.ParentAgentID == nil && childOf(a) == ""
}

// wsView is a workspace plus everything the UI derives for it.
type wsView struct {
	ws       *Workspace
	agents   []Agent // tabs, oldest first
	bucket   bucket
	since    time.Time // when it entered that state
	activity time.Time
	running  time.Time // turn start of the running agent, if any
	num      int       // position under its sidebar heading (project, status or Pinned), from 1
}

// title is the workspace's name with its position under its sidebar heading,
// e.g. "2. main", so moving through the sidebar shows which way you're going.
func (v *wsView) title() string {
	if v.num == 0 {
		return v.ws.DisplayName()
	}
	return fmt.Sprintf("%d. %s", v.num, v.ws.DisplayName())
}

type projView struct {
	id, name   string
	root       string // project root path (on the daemon's machine)
	git        bool   // worktrees are only possible in git projects
	workspaces []*wsView
	activity   time.Time
	empty      bool
}

// buildWorkspaces joins workspaces and agents into the sidebar model.
func (m *Model) buildWorkspaces() {
	byID := map[string]*wsView{}
	var views []*wsView
	for i := range m.workspaces {
		w := &m.workspaces[i]
		if w.Background && !m.showAll {
			continue
		}
		v := &wsView{ws: w}
		byID[w.ID] = v
		views = append(views, v)
	}
	// agents attach to their workspace; unknown workspaces are synthesized
	for _, a := range m.agents {
		key := ""
		if a.WorkspaceID != nil {
			key = *a.WorkspaceID
		}
		if key == "" {
			key = "cwd:" + a.Cwd
		}
		v := byID[key]
		if v == nil {
			if !m.wsKnown && a.Snap.Background && !m.showAll {
				continue
			}
			w := &Workspace{ID: key, Name: filepath.Base(a.Cwd), WorkspaceDirectory: a.Cwd, Status: "done", synthetic: true}
			if p := a.Snap.Project; p != nil {
				w.ProjectDisplayName, w.ProjectID = p.ProjectName, p.ProjectKey
				if p.WorkspaceName != nil && *p.WorkspaceName != "" {
					w.Name = *p.WorkspaceName
				}
			} else {
				w.ProjectDisplayName, w.ProjectID = filepath.Base(a.Cwd), "cwd:"+a.Cwd
			}
			if strings.Contains(a.Cwd, "/.paseo/worktrees/") {
				w.WorkspaceKind = "worktree"
			}
			v = &wsView{ws: w}
			byID[key] = v
			views = append(views, v)
		}
		if rootTabAgent(a) || m.showAll {
			v.agents = append(v.agents, a)
		}
	}

	for _, v := range views {
		sort.SliceStable(v.agents, func(i, j int) bool { return v.agents[i].CreatedAt.Before(v.agents[j].CreatedAt) })
		w := v.ws
		v.bucket, v.since = bucketFromString(w.Status), w.StatusEnteredAt.Time
		// newest agent activity drives the bucket when the daemon says "done"
		var latest *Agent
		for i := range v.agents {
			a := &v.agents[i]
			if latest == nil || a.UpdatedAt.After(latest.UpdatedAt) {
				latest = a
			}
			if a.UpdatedAt.After(v.activity) {
				v.activity = a.UpdatedAt
			}
			if t := m.turnStarted(*a); !t.IsZero() && (v.running.IsZero() || t.Before(v.running)) {
				v.running = t
			}
		}
		// any agent needing input or failing outranks a calm workspace status
		for _, a := range v.agents {
			if b := agentBucket(a.Snap); b > v.bucket {
				v.bucket = b
				if v.since.IsZero() || b == bRunning {
					v.since = a.UpdatedAt
				}
			}
		}
		if v.bucket == bDone && latest != nil && v.since.IsZero() {
			v.since = latest.UpdatedAt
		}
		for _, t := range []time.Time{w.ActivityAt.Time, w.LastUserMessageAt.Time, w.StatusEnteredAt.Time, w.CreatedAt.Time} {
			if t.After(v.activity) {
				v.activity = t
			}
		}
	}

	// projects
	projByID := map[string]*projView{}
	var projects []*projView
	var pinned []*wsView
	for _, v := range views {
		if !v.ws.PinnedAt.IsZero() {
			pinned = append(pinned, v)
		}
		id := v.ws.ProjectID
		if id == "" {
			id = v.ws.ProjectName()
		}
		p := projByID[id]
		if p == nil {
			p = &projView{id: id, name: v.ws.ProjectName()}
			projByID[id] = p
			projects = append(projects, p)
		}
		if v.ws.PinnedAt.IsZero() {
			p.workspaces = append(p.workspaces, v)
		}
		if p.root == "" && !v.ws.synthetic {
			p.root = v.ws.ProjectRootPath
			p.git = v.ws.ProjectKind == "git" || v.ws.ProjectKind == ""
		}
		if v.activity.After(p.activity) {
			p.activity = v.activity
		}
	}
	for _, e := range m.emptyProjects {
		if projByID[e.ProjectID] != nil {
			continue
		}
		name := e.ProjectDisplayName
		if e.ProjectCustomName != nil && *e.ProjectCustomName != "" {
			name = *e.ProjectCustomName
		}
		p := &projView{id: e.ProjectID, name: name, root: e.ProjectRootPath, empty: true,
			git: e.ProjectKind == "git" || e.ProjectKind == ""}
		projByID[e.ProjectID] = p
		projects = append(projects, p)
	}
	for _, p := range projects {
		sort.SliceStable(p.workspaces, func(i, j int) bool { return p.workspaces[i].activity.After(p.workspaces[j].activity) })
	}
	// most recently active project first; empty projects last, alphabetically
	sort.SliceStable(projects, func(i, j int) bool {
		a, b := projects[i], projects[j]
		if a.activity.Equal(b.activity) {
			return strings.ToLower(a.name) < strings.ToLower(b.name)
		}
		return a.activity.After(b.activity)
	})
	sort.SliceStable(pinned, func(i, j int) bool { return pinned[i].ws.PinnedAt.After(pinned[j].ws.PinnedAt.Time) })

	m.wsViews, m.projects, m.pinned = byID, projects, pinned
}

func (m *Model) wsOfAgent(id string) *wsView {
	for _, v := range m.wsViews {
		for _, a := range v.agents {
			if a.ID == id {
				return v
			}
		}
	}
	return nil
}

func (m *Model) projectAggregate(p *projView) bucket {
	// bucket values are ordered by urgency: needs input > failed > running > attention > done
	b := bDone
	for _, v := range p.workspaces {
		b = max(b, v.bucket)
	}
	return b
}
