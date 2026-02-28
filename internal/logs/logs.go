package logs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"tenazas/internal/events"
	"tenazas/internal/models"
	"tenazas/internal/session"
)

// Filter holds the query parameters for log retrieval.
type Filter struct {
	Type      string // filter by audit type (e.g., "llm_response")
	Role      string // filter by conversation role (user/assistant/system)
	Step      string // filter by skill step tag (e.g., "loop.step_8_pr")
	Since     time.Time
	Until     time.Time
	Search    string // text search in content
	Heartbeat string // filter by heartbeat name (finds matching sessions)
}

// Summary holds aggregated statistics for a session's audit log.
type Summary struct {
	SessionID      string
	Title          string
	Status         string
	Duration       time.Duration
	FirstEntry     time.Time
	LastEntry      time.Time
	StatesVisited  []string
	TotalEntries   int
	PromptCount    int
	ResponseCount  int
	ThoughtCount   int
	CmdResultCount int
	ErrorCount     int
	RetryCount     int
	StatusChanges  int
	Interventions  int
}

// ReadAuditFile reads all audit entries from a JSONL file, applying the given filter.
func ReadAuditFile(path string, f *Filter) ([]events.AuditEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []events.AuditEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry events.AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if matchesFilter(entry, f) {
			entries = append(entries, entry)
		}
	}

	return entries, scanner.Err()
}

func matchesFilter(entry events.AuditEntry, f *Filter) bool {
	if f == nil {
		return true
	}
	if f.Type != "" && entry.Type != f.Type {
		return false
	}
	if f.Role != "" && entry.Role != f.Role {
		return false
	}
	if f.Step != "" && entry.Step != f.Step {
		return false
	}
	if !f.Since.IsZero() && entry.Timestamp.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && entry.Timestamp.After(f.Until) {
		return false
	}
	if f.Search != "" && !strings.Contains(strings.ToLower(entry.Content), strings.ToLower(f.Search)) {
		return false
	}
	return true
}

// Summarize computes aggregated statistics from a list of audit entries.
func Summarize(entries []events.AuditEntry, sess *models.Session) Summary {
	s := Summary{
		TotalEntries: len(entries),
	}
	if sess != nil {
		s.SessionID = sess.ID
		s.Title = sess.Title
		s.Status = sess.Status
	}

	stateSet := make(map[string]bool)

	for _, e := range entries {
		if s.FirstEntry.IsZero() || e.Timestamp.Before(s.FirstEntry) {
			s.FirstEntry = e.Timestamp
		}
		if e.Timestamp.After(s.LastEntry) {
			s.LastEntry = e.Timestamp
		}

		switch e.Type {
		case events.AuditLLMPrompt:
			s.PromptCount++
		case events.AuditLLMResponse:
			s.ResponseCount++
		case events.AuditLLMThought:
			s.ThoughtCount++
		case events.AuditCmdResult:
			s.CmdResultCount++
			if e.ExitCode != 0 {
				s.ErrorCount++
			}
		case events.AuditStatus:
			s.StatusChanges++
			// Extract state names from status messages
			if strings.Contains(e.Content, "at node") {
				parts := strings.SplitAfter(e.Content, "at node ")
				if len(parts) > 1 {
					node := strings.TrimSpace(parts[1])
					stateSet[node] = true
				}
			}
		case events.AuditIntervention:
			s.Interventions++
		case events.AuditInfo:
			if strings.Contains(e.Content, "Fail route") || strings.HasPrefix(e.Content, "LLM Error") {
				s.RetryCount++
			}
		}
	}

	if !s.FirstEntry.IsZero() && !s.LastEntry.IsZero() {
		s.Duration = s.LastEntry.Sub(s.FirstEntry)
	}

	for node := range stateSet {
		s.StatesVisited = append(s.StatesVisited, node)
	}

	return s
}

// FindHeartbeatSessions returns sessions whose title matches the heartbeat naming convention.
func FindHeartbeatSessions(sm *session.Manager, heartbeatName string) ([]*models.Session, error) {
	title := "Heartbeat: " + heartbeatName

	var matches []*models.Session
	page := 0
	for {
		sessions, total, err := sm.List(page, 50)
		if err != nil {
			return nil, err
		}
		for i := range sessions {
			if sessions[i].Title == title {
				s := sessions[i]
				matches = append(matches, &s)
			}
		}
		if (page+1)*50 >= total || len(sessions) == 0 {
			break
		}
		page++
	}
	return matches, nil
}

// FormatEntry formats a single audit entry for terminal display with role badges and timestamps.
func FormatEntry(e events.AuditEntry) string {
	ts := e.Timestamp.Format("15:04:05")

	roleBadge := roleBadgeFor(e.Role)
	typeBadge := typeBadgeFor(e.Type)

	stepLabel := ""
	if e.Step != "" {
		stepLabel = fmt.Sprintf(" \x1b[36m<%s>\x1b[0m", e.Step)
	}

	sourceLabel := ""
	if e.Source != "" && e.Source != "engine" {
		sourceLabel = fmt.Sprintf(" \x1b[2m[%s]\x1b[0m", e.Source)
	}

	modelLabel := ""
	if e.Model != "" {
		modelLabel = fmt.Sprintf(" \x1b[35m(%s)\x1b[0m", e.Model)
	} else if e.ModelTier != "" {
		modelLabel = fmt.Sprintf(" \x1b[35m(tier:%s)\x1b[0m", e.ModelTier)
	}

	content := e.Content
	if len(content) > 500 {
		content = content[:497] + "..."
	}

	exitInfo := ""
	if e.Type == events.AuditCmdResult && e.ExitCode != 0 {
		exitInfo = fmt.Sprintf(" \x1b[31m(exit %d)\x1b[0m", e.ExitCode)
	}

	return fmt.Sprintf("\x1b[2m%s\x1b[0m %s%s%s%s%s%s %s",
		ts, roleBadge, typeBadge, stepLabel, modelLabel, sourceLabel, exitInfo, content)
}

func roleBadgeFor(role string) string {
	switch role {
	case events.RoleUser:
		return "\x1b[34m👤\x1b[0m "
	case events.RoleAssistant:
		return "\x1b[32m🤖\x1b[0m "
	case events.RoleSystem:
		return "\x1b[33m⚙\x1b[0m  "
	default:
		return "   "
	}
}

func typeBadgeFor(typ string) string {
	switch typ {
	case events.AuditLLMPrompt:
		return "\x1b[34mPROMPT\x1b[0m"
	case events.AuditLLMResponse:
		return "\x1b[32mRESPONSE\x1b[0m"
	case events.AuditLLMChunk:
		return "\x1b[2mCHUNK\x1b[0m"
	case events.AuditLLMThought:
		return "\x1b[2mTHOUGHT\x1b[0m"
	case events.AuditCmdResult:
		return "\x1b[33mCMD\x1b[0m"
	case events.AuditIntent:
		return "\x1b[36mINTENT\x1b[0m"
	case events.AuditIntervention:
		return "\x1b[31;1mINTERVENTION\x1b[0m"
	case events.AuditStatus:
		return "\x1b[35mSTATUS\x1b[0m"
	case events.AuditInfo:
		return "\x1b[2mINFO\x1b[0m"
	default:
		return typ
	}
}

// FormatSummary formats a Summary for terminal display.
func FormatSummary(s Summary) string {
	var b strings.Builder

	title := s.Title
	if title == "" {
		title = s.SessionID
	}

	b.WriteString(fmt.Sprintf("\x1b[1mSession: %s\x1b[0m", title))
	if s.SessionID != "" && s.Title != "" {
		b.WriteString(fmt.Sprintf(" \x1b[2m(%s)\x1b[0m", s.SessionID))
	}
	b.WriteString("\n")

	statusColor := "\x1b[32m"
	if s.Status == models.StatusFailed {
		statusColor = "\x1b[31m"
	} else if s.Status == models.StatusIntervention {
		statusColor = "\x1b[33m"
	}
	b.WriteString(fmt.Sprintf("Status: %s%s\x1b[0m | Duration: %s | Entries: %d\n",
		statusColor, s.Status, formatDuration(s.Duration), s.TotalEntries))

	b.WriteString(fmt.Sprintf("Prompts: %d | Responses: %d | Thoughts: %d | Commands: %d\n",
		s.PromptCount, s.ResponseCount, s.ThoughtCount, s.CmdResultCount))

	if s.ErrorCount > 0 || s.RetryCount > 0 || s.Interventions > 0 {
		b.WriteString(fmt.Sprintf("\x1b[31mErrors: %d\x1b[0m | Retries: %d | Interventions: %d\n",
			s.ErrorCount, s.RetryCount, s.Interventions))
	}

	if len(s.StatesVisited) > 0 {
		b.WriteString(fmt.Sprintf("States: %s\n", strings.Join(s.StatesVisited, " → ")))
	}

	return b.String()
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "< 1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", m, s)
}
