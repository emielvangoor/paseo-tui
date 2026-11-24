package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Bridge runs bridge/dist/paseo-bridge.mjs, which keeps one live connection to
// the daemon through Paseo's own client library, and exchanges NDJSON with it.
type Bridge struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	nextID  int
	pending map[int]chan bridgeRes
	Events  chan BridgeMsg
	stderr  bytes.Buffer
}

// BridgeMsg is one line from the bridge.
type BridgeMsg struct {
	Type          string            `json:"type"`
	Host          string            `json:"host"`
	State         string            `json:"state"`
	Code          string            `json:"code"`
	Message       string            `json:"message"`
	AgentID       string            `json:"agentId"`
	Agents        []Snapshot        `json:"agents"`
	Workspaces    []Workspace       `json:"workspaces"`
	EmptyProjects []EmptyProject    `json:"emptyProjects"`
	Items         []json.RawMessage `json:"items"`
	Event         json.RawMessage   `json:"event"`
	Timestamp     string            `json:"timestamp"`
	ID            int               `json:"id"`
	OK            bool              `json:"ok"`
	Error         string            `json:"error"`
	Data          json.RawMessage   `json:"data"`
	Payload       json.RawMessage   `json:"payload"`
}

type bridgeRes struct {
	data json.RawMessage
	err  error
}

// Snapshot is the daemon's agent snapshot (subset we use).
type Snapshot struct {
	ID          string  `json:"id"`
	Provider    string  `json:"provider"`
	Cwd         string  `json:"cwd"`
	WorkspaceID *string `json:"workspaceId"`
	Model       *string `json:"model"`
	RuntimeInfo *struct {
		Model *string `json:"model"`
	} `json:"runtimeInfo"`
	Thinking           *string           `json:"effectiveThinkingOptionId"`
	CreatedAt          time.Time         `json:"createdAt"`
	UpdatedAt          time.Time         `json:"updatedAt"`
	Status             string            `json:"status"`
	Title              *string           `json:"title"`
	ArchivedAt         *string           `json:"archivedAt"`
	RequiresAttention  bool              `json:"requiresAttention"`
	AttentionReason    *string           `json:"attentionReason"`
	CurrentModeID      *string           `json:"currentModeId"`
	PendingPermissions []PermissionReq   `json:"pendingPermissions"`
	ActiveTurn         *ActiveTurn       `json:"activeTurn"`
	LastUsage          *Usage            `json:"lastUsage"`
	LastError          *string           `json:"lastError"`
	Project            *ProjectPlacement `json:"project"`
	Background         bool              `json:"background"`
	Labels             map[string]string `json:"labels"`
	ThinkingOptionID   *string           `json:"thinkingOptionId"`
	AvailableModes     []AgentMode       `json:"availableModes"`
	Features           []AgentFeature    `json:"features"`
	ParentAgentID      *string           `json:"parentAgentId"`
	BackgroundWork     *BackgroundWork   `json:"backgroundWork"`
}

type BackgroundWork struct {
	Count        int  `json:"count"`
	RunningCount *int `json:"runningCount"`
}

func (b *BackgroundWork) running() int {
	if b.RunningCount != nil {
		return *b.RunningCount
	}
	return b.Count
}

type ActiveTurn struct {
	TurnID    string     `json:"turnId"`
	StartedAt *time.Time `json:"startedAt"`
}

type Usage struct {
	InputTokens            int64   `json:"inputTokens"`
	CachedInputTokens      int64   `json:"cachedInputTokens"`
	OutputTokens           int64   `json:"outputTokens"`
	TotalCostUsd           float64 `json:"totalCostUsd"`
	ContextWindowMaxTokens int64   `json:"contextWindowMaxTokens"`
	ContextWindowUsed      int64   `json:"contextWindowUsedTokens"`
}

type ProjectPlacement struct {
	ProjectKey    string  `json:"projectKey"`
	ProjectName   string  `json:"projectName"`
	WorkspaceName *string `json:"workspaceName"`
}

type PermissionReq struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Detail      json.RawMessage `json:"detail"`
}

func StartBridge(node, script string, args, env []string) (*Bridge, error) {
	cmd := exec.Command(node, append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	b := &Bridge{cmd: cmd, stdin: stdin, pending: map[int]chan bridgeRes{}, Events: make(chan BridgeMsg, 1024)}
	cmd.Stderr = &limitedWriter{buf: &b.stderr, max: 8192}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go b.read(stdout)
	return b, nil
}

func (b *Bridge) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var m BridgeMsg
			if json.Unmarshal(line, &m) == nil {
				if m.Type == "res" {
					b.mu.Lock()
					ch := b.pending[m.ID]
					delete(b.pending, m.ID)
					b.mu.Unlock()
					if ch != nil {
						if m.OK {
							ch <- bridgeRes{data: m.Data}
						} else {
							ch <- bridgeRes{err: errors.New(m.Error)}
						}
					}
				} else {
					b.Events <- m
				}
			}
		}
		if err != nil {
			break
		}
	}
	werr := b.cmd.Wait()
	msg := "bridge exited"
	if s := firstLine(b.stderr.String()); s != "" {
		msg += ": " + s
	} else if werr != nil {
		msg += ": " + werr.Error()
	}
	b.mu.Lock()
	for id, ch := range b.pending {
		ch <- bridgeRes{err: errors.New(msg)}
		delete(b.pending, id)
	}
	b.mu.Unlock()
	b.Events <- BridgeMsg{Type: "exit", Message: msg}
	close(b.Events)
}

// Call sends a request and waits for its response.
func (b *Bridge) Call(op string, fields map[string]any, timeout time.Duration) (json.RawMessage, error) {
	b.mu.Lock()
	b.nextID++
	id := b.nextID
	ch := make(chan bridgeRes, 1)
	b.pending[id] = ch
	req := map[string]any{"id": id, "op": op}
	for k, v := range fields {
		req[k] = v
	}
	line, _ := json.Marshal(req)
	_, err := b.stdin.Write(append(line, '\n'))
	b.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("bridge not running")
	}
	select {
	case r := <-ch:
		return r.data, r.err
	case <-time.After(timeout):
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("%s timed out", op)
	}
}

func (b *Bridge) Close() {
	_ = b.stdin.Close()
	go func() {
		time.Sleep(2 * time.Second)
		if b.cmd.Process != nil {
			_ = b.cmd.Process.Kill()
		}
	}()
}

type limitedWriter struct {
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.buf.Len() < w.max {
		w.buf.Write(p[:min(len(p), w.max-w.buf.Len())])
	}
	return len(p), nil
}

// ---------- snapshot -> Agent ----------

func (s Snapshot) toAgent() Agent {
	a := Agent{
		ID:          s.ID,
		ShortID:     s.ID[:min(7, len(s.ID))],
		Name:        deref(s.Title),
		Provider:    s.Provider,
		Thinking:    deref(s.Thinking),
		Status:      s.Status,
		Cwd:         s.Cwd,
		WorkspaceID: s.WorkspaceID,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
		Snap:        s,
	}
	model := ""
	if s.RuntimeInfo != nil && s.RuntimeInfo.Model != nil {
		model = *s.RuntimeInfo.Model
	} else if s.Model != nil {
		model = *s.Model
	}
	if model != "" && model != "default" {
		a.Provider += "/" + model
	}
	if a.Thinking == "" {
		a.Thinking = "auto"
	}
	a.Created = ago(s.CreatedAt)
	return a
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
