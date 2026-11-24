package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Prompt history for the message box: ↑ on the first line recalls older
// prompts, ↓ on the last line walks back to the draft. Candidates are this
// agent's earlier prompts (from its transcript) followed by everything sent
// from paseo-tui, newest first.

const historyMax = 500

type promptHistory struct {
	sent  []string // persisted, oldest first
	list  []string // candidates for the current browse, newest first
	idx   int      // -1 = editing the draft
	draft string
}

func historyPath() string {
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if x := os.Getenv("XDG_STATE_HOME"); x != "" {
		return filepath.Join(x, "paseo-tui", "history.json")
	}
	return filepath.Join(dir, ".local", "state", "paseo-tui", "history.json")
}

func loadHistory() *promptHistory {
	h := &promptHistory{idx: -1}
	if b, err := os.ReadFile(historyPath()); err == nil {
		_ = json.Unmarshal(b, &h.sent)
	}
	return h
}

func (h *promptHistory) add(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if n := len(h.sent); n > 0 && h.sent[n-1] == text {
		h.reset()
		return
	}
	h.sent = append(h.sent, text)
	if len(h.sent) > historyMax {
		h.sent = h.sent[len(h.sent)-historyMax:]
	}
	h.reset()
	if p := historyPath(); p != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if b, err := json.Marshal(h.sent); err == nil {
			_ = os.WriteFile(p, b, 0o600)
		}
	}
}

func (h *promptHistory) reset() { h.idx, h.list, h.draft = -1, nil, "" }

// older returns the previous entry, starting a browse from draft when needed.
func (h *promptHistory) older(draft string, agentPrompts []string) (string, bool) {
	if h.idx == -1 {
		h.draft = draft
		h.list = h.list[:0]
		seen := map[string]bool{}
		add := func(s string) {
			s = strings.TrimSpace(s)
			if s != "" && !seen[s] {
				seen[s] = true
				h.list = append(h.list, s)
			}
		}
		for i := len(agentPrompts) - 1; i >= 0; i-- {
			add(agentPrompts[i])
		}
		for i := len(h.sent) - 1; i >= 0; i-- {
			add(h.sent[i])
		}
	}
	if h.idx+1 >= len(h.list) {
		return "", false
	}
	h.idx++
	return h.list[h.idx], true
}

// newer returns the next entry, or the draft once past the newest.
func (h *promptHistory) newer() (string, bool) {
	if h.idx < 0 {
		return "", false
	}
	h.idx--
	if h.idx < 0 {
		d := h.draft
		h.reset()
		return d, true
	}
	return h.list[h.idx], true
}
