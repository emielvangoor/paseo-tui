package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Plan usage (U), like the desktop's: for each provider account the host
// knows, its quota windows (session, weekly, …), balances and details, as the
// daemon reports them. r asks the providers again instead of the host's cache.

type UsageWindow struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	UsedPct         *float64 `json:"usedPct"`
	RemainingPct    *float64 `json:"remainingPct"`
	ResetsAt        string   `json:"resetsAt"`
	DurationSeconds float64  `json:"durationSeconds"`
	RunsOutAt       string   `json:"runsOutAt"`
	Tone            string   `json:"tone"`
}

type UsageBalance struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	Used      *float64 `json:"used"`
	Remaining *float64 `json:"remaining"`
	Limit     *float64 `json:"limit"`
	Unit      string   `json:"unit"` // usd | credits | requests | tokens
	ResetsAt  string   `json:"resetsAt"`
	Tone      string   `json:"tone"`
}

type UsageDetail struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Tone  string `json:"tone"`
}

type UsageProblem struct {
	Kind        string `json:"kind"` // expired | rejected | no_quota
	ExpiresAt   string `json:"expiresAt"`
	RefreshedBy string `json:"refreshedBy"`
	Status      int    `json:"status"`
	Detail      string `json:"detail"`
}

type UsageReport struct {
	Status     string         `json:"status"` // available | unavailable | error
	PlanLabel  string         `json:"planLabel"`
	ObservedAt string         `json:"observedAt"`
	Windows    []UsageWindow  `json:"windows"`
	Balances   []UsageBalance `json:"balances"`
	Details    []UsageDetail  `json:"details"`
	Problem    *UsageProblem  `json:"problem"`
	Error      string         `json:"error"`
}

type UsageEntry struct {
	ID      string `json:"id"`
	Account struct {
		Label string `json:"label"`
	} `json:"account"`
	FetchedAt      string      `json:"fetchedAt"`
	SourceLabel    string      `json:"sourceLabel"`
	Report         UsageReport `json:"report"`
	LastSuccessful *struct {
		FetchedAt string      `json:"fetchedAt"`
		Report    UsageReport `json:"report"`
	} `json:"lastSuccessful"`
}

type usagePanel struct {
	entries []UsageEntry
	loading bool // a request is out
	err     string
	scroll  int
	at      time.Time // when entries arrived
}

type usageMsg struct {
	entries []UsageEntry
	err     error
}

func (m *Model) openUsage() (tea.Model, tea.Cmd) {
	if m.br == nil || !m.connected {
		m.setStatus("Not connected to the daemon", true)
		return *m, nil
	}
	if m.usage == nil {
		m.usage = &usagePanel{}
	}
	m.usage.scroll, m.usage.err = 0, ""
	m.overlay = ovUsage
	if m.focus == focusInput {
		m.focus, m.slash = focusList, nil
		m.input.Blur()
	}
	return *m, m.loadUsage(false)
}

// loadUsage asks the host; refresh makes it ask the providers rather than
// answer from its cache.
func (m *Model) loadUsage(refresh bool) tea.Cmd {
	if m.usage.loading {
		return nil
	}
	m.usage.loading = true
	br := m.br
	return func() tea.Msg {
		data, err := br.Call("usage", map[string]any{"refresh": refresh}, 90*time.Second)
		var entries []UsageEntry
		if err == nil {
			err = json.Unmarshal(data, &entries)
		}
		return usageMsg{entries: entries, err: err}
	}
}

func (m *Model) gotUsage(msg usageMsg) {
	u := m.usage
	if u == nil {
		return
	}
	u.loading = false
	if msg.err != nil {
		u.err = msg.err.Error()
		return
	}
	u.err, u.entries, u.at = "", msg.entries, time.Now()
}

func (m *Model) usageKey(k tea.KeyMsg) tea.Cmd {
	u := m.usage
	switch k.String() {
	case "esc", "q", "U":
		m.overlay = ovNone
	case "r":
		return m.loadUsage(true)
	case "j", "down":
		u.scroll++
	case "k", "up":
		u.scroll = max(0, u.scroll-1)
	case "pgdown", "ctrl+d", " ":
		u.scroll += 10
	case "pgup", "ctrl+u":
		u.scroll = max(0, u.scroll-10)
	case "g", "home":
		u.scroll = 0
	case "G", "end":
		u.scroll = math.MaxInt32
	}
	return nil
}

func (m Model) usageView() string {
	u := m.usage
	w := max(60, min(104, m.width-8))
	inner := w - 4
	now := time.Now()
	var body []string
	switch {
	case u.err != "" && len(u.entries) == 0:
		body = append(body, sRed.Render(" "+truncate(u.err, inner-2)))
	case u.loading && len(u.entries) == 0:
		body = append(body, " "+m.spin.View()+sMuted.Render(" loading usage…"))
	case len(u.entries) == 0:
		body = append(body, sMuted.Render(" no usage data"))
	}
	labelW := usageLabelWidth(u.entries)
	for i, e := range u.entries {
		if i > 0 {
			body = append(body, "")
		}
		body = append(body, usageCard(e, inner, labelW, now)...)
	}

	budget := max(5, m.height-10)
	u.scroll = min(u.scroll, max(0, len(body)-budget))
	shown := body[u.scroll:min(len(body), u.scroll+budget)]

	state := ""
	switch {
	case u.loading:
		state = m.spin.View() + sMuted.Render(" refreshing")
	case u.err != "":
		state = sRed.Render("refresh failed")
	case !u.at.IsZero():
		state = sMuted.Render("updated " + agoPhrase(u.at))
	}
	lines := []string{rightAlign(sAccent.Render(" Plan usage"), state, inner+1), ""}
	lines = append(lines, shown...)
	foot := "r refresh · esc close"
	if len(body) > budget {
		foot = "↑↓ scroll · " + foot
	}
	lines = append(lines, "", sMuted.Render(" "+foot))
	return frame(lines, w)
}

// usageCard is one account: its name and plan, then its windows, balances and
// details, or why there's nothing to show.
func usageCard(e UsageEntry, w, labelW int, now time.Time) []string {
	name := sBold.Render(e.SourceLabel)
	if e.Account.Label != "" {
		name += sMuted.Render(" · " + e.Account.Label)
	}
	r := e.Report
	plan := ""
	if r.Status == "available" && r.PlanLabel != "" {
		plan = sMuted.Render(r.PlanLabel)
	}
	out := []string{rightAlign(" "+truncate(name, w-lipgloss.Width(plan)-3), plan, w+1)}
	switch r.Status {
	case "available":
		out = append(out, usageRows(r, w, labelW, now)...)
	case "unavailable":
		msg := "Unavailable"
		if r.Problem != nil {
			msg = usageProblemText(*r.Problem, now)
		}
		out = append(out, "   "+sYellow.Render(truncate(msg, w-4)))
	default:
		out = append(out, "   "+sRed.Render(truncate(firstLine(r.Error), w-4)))
	}
	if r.Status != "available" && e.LastSuccessful != nil {
		t, _ := time.Parse(time.RFC3339, e.LastSuccessful.FetchedAt)
		out = append(out, "   "+sMuted.Render("last reading, "+agoPhrase(t)+":"))
		out = append(out, usageRows(e.LastSuccessful.Report, w, labelW, now)...)
	}
	if len(out) == 1 {
		out = append(out, "   "+sMuted.Render("no usage data"))
	}
	return out
}

// usageLabelWidth is the widest row label of all the accounts, so their bars line up.
func usageLabelWidth(entries []UsageEntry) int {
	w := 0
	for _, e := range entries {
		reports := []UsageReport{e.Report}
		if e.LastSuccessful != nil {
			reports = append(reports, e.LastSuccessful.Report)
		}
		for _, r := range reports {
			for _, x := range r.Windows {
				w = max(w, lipgloss.Width(x.Label))
			}
			for _, b := range r.Balances {
				w = max(w, lipgloss.Width(b.Label))
			}
			for _, d := range r.Details {
				w = max(w, lipgloss.Width(d.Label))
			}
		}
	}
	return min(w, 18)
}

func usageRows(r UsageReport, w, labelW int, now time.Time) []string {
	label := func(s string) string { return "   " + padTo(truncate(s, labelW), labelW) + "  " }
	// the bar leaves room for "100% used" and a reset countdown
	barW := max(8, min(30, w-labelW-5-10-36))

	var out []string
	for _, x := range r.Windows {
		used, ok := usedPct(x)
		tone := x.Tone
		if tone == "" && ok {
			tone = toneFor(used)
		}
		pct, trail := sMuted.Render("–"), []string{}
		if ok {
			pct = toneStyle(tone).Render(fmt.Sprintf("%3.0f%% used", math.Max(0, math.Min(100, used))))
		}
		if p := usagePace(x, r.ObservedAt, now); p != "" {
			trail = append(trail, sRed.Render(p))
		}
		if s := resetPhrase(x.ResetsAt, now); s != "" {
			trail = append(trail, sMuted.Render(s))
		}
		tail := strings.Join(trail, sMuted.Render(" · "))
		line := label(x.Label) + usageBar(used, ok, tone, barW) + " " + pct
		if tail != "" {
			line += "  " + tail
		}
		out = append(out, truncate(line, w))
	}
	for _, b := range r.Balances {
		line := label(b.Label) + toneStyle(b.Tone).Render(balanceText(b))
		if s := resetPhrase(b.ResetsAt, now); s != "" {
			line += sMuted.Render("  " + s)
		}
		out = append(out, truncate(line, w))
	}
	for _, d := range r.Details {
		out = append(out, truncate(label(d.Label)+toneStyle(d.Tone).Render(d.Value), w))
	}
	return out
}

func usedPct(x UsageWindow) (float64, bool) {
	switch {
	case x.UsedPct != nil:
		return *x.UsedPct, true
	case x.RemainingPct != nil:
		return 100 - *x.RemainingPct, true
	}
	return 0, false
}

// toneFor is the desktop's tone for a window without one of its own.
func toneFor(used float64) string {
	switch {
	case used > 90:
		return "danger"
	case used >= 70:
		return "warning"
	}
	return "default"
}

func toneStyle(tone string) lipgloss.Style {
	switch tone {
	case "ok":
		return sGreen
	case "warning":
		return sYellow
	case "danger":
		return sRed
	}
	return lipgloss.NewStyle()
}

func usageBar(used float64, ok bool, tone string, w int) string {
	filled := 0
	if ok {
		filled = int(math.Round(math.Max(0, math.Min(100, used)) / 100 * float64(w)))
	}
	col := toneStyle(tone)
	if tone == "" || tone == "default" {
		col = sAccent
	}
	return col.Render(strings.Repeat("█", filled)) + sMuted.Foreground(cBorder).Render(strings.Repeat("░", w-filled))
}

func balanceText(b UsageBalance) string {
	f := func(v float64) string { return formatAmount(v, b.Unit) }
	switch {
	case b.Limit != nil && *b.Limit > 0:
		used := "—"
		if b.Used != nil {
			used = f(*b.Used)
		} else if b.Remaining != nil {
			used = f(*b.Limit - *b.Remaining)
		}
		return used + " / " + f(*b.Limit)
	case b.Remaining != nil:
		return f(*b.Remaining) + " left"
	case b.Used != nil:
		return f(*b.Used)
	}
	return "—"
}

func formatAmount(v float64, unit string) string {
	switch unit {
	case "usd":
		return fmt.Sprintf("$%.2f", v)
	case "tokens":
		return humanTokens(int64(v))
	}
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// resetPhrase is "resets in 2d 3h", "resets in 37m", or "resetting now".
func resetPhrase(iso string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, iso)
	if iso == "" || err != nil {
		return ""
	}
	d := t.Sub(now)
	if d <= 0 {
		return "resetting now"
	}
	return "resets in " + shortDuration(d)
}

// shortDuration is the two largest units, floored: "2d 23h", "4h 59m", "37m".
func shortDuration(d time.Duration) string {
	days, hours, mins := int(d.Hours())/24, int(d.Hours())%24, int(d.Minutes())%60
	pair := func(a int, au string, b int, bu string) string {
		if b == 0 {
			return fmt.Sprintf("%d%s", a, au)
		}
		return fmt.Sprintf("%d%s %d%s", a, au, b, bu)
	}
	switch {
	case days == 1 && hours == 0:
		return "24h"
	case days > 0:
		return pair(days, "d", hours, "h")
	case hours > 0:
		return pair(hours, "h", mins, "m")
	}
	return fmt.Sprintf("%dm", mins)
}

// usagePace is the desktop's warning for a window: the limit is reached, or at
// the pace so far it runs out before it resets.
func usagePace(x UsageWindow, observedAt string, now time.Time) string {
	used, ok := usedPct(x)
	if ok && used >= 100 {
		return "limit reached"
	}
	reset, err := time.Parse(time.RFC3339, x.ResetsAt)
	if err != nil || !reset.After(now) {
		return ""
	}
	runsOut := func(at time.Time) string {
		if !at.After(now) {
			return "may have run out"
		}
		return "runs out " + clockPhrase(at, now)
	}
	if at, err := time.Parse(time.RFC3339, x.RunsOutAt); err == nil && at.Before(reset) {
		return runsOut(at)
	}
	sampled, err := time.Parse(time.RFC3339, observedAt)
	if err != nil || !ok || used <= 0 || x.DurationSeconds <= 0 {
		return ""
	}
	duration := time.Duration(x.DurationSeconds * float64(time.Second))
	elapsed := sampled.Sub(reset.Add(-duration))
	minimum := max(time.Minute, min(duration/100, time.Hour))
	if !sampled.Before(reset) || elapsed < minimum {
		return ""
	}
	if used/elapsed.Seconds()*duration.Seconds() > 100 {
		return runsOut(sampled.Add(time.Duration((100 - used) / used * float64(elapsed))))
	}
	return ""
}

// clockPhrase is "14:05" today, "Mon 14:05" this week, else "Mon Dec 8, 14:05".
func clockPhrase(t, now time.Time) string {
	t = t.Local()
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	days := int(time.Date(y2, m2, d2, 0, 0, 0, 0, time.Local).Sub(time.Date(y1, m1, d1, 0, 0, 0, 0, time.Local)).Hours() / 24)
	switch {
	case days == 0:
		return t.Format("15:04")
	case days <= 6:
		return t.Format("Mon 15:04")
	}
	return t.Format("Mon Jan 2, 15:04")
}

func usageProblemText(p UsageProblem, now time.Time) string {
	if p.Kind == "no_quota" {
		return p.Detail
	}
	remedy := "Sign in again."
	if p.RefreshedBy != "" {
		remedy = "Run " + p.RefreshedBy + " to refresh it."
	}
	if p.Kind == "rejected" {
		return fmt.Sprintf("Login rejected (HTTP %d). %s", p.Status, remedy)
	}
	t, _ := time.Parse(time.RFC3339, p.ExpiresAt)
	return "Login expired " + agoPhrase(t) + ". " + remedy
}

func padTo(s string, w int) string {
	return s + strings.Repeat(" ", max(0, w-lipgloss.Width(s)))
}
