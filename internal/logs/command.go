package logs

import (
	"flag"
	"fmt"
	"os"
	"time"

	"tenazas/internal/events"
	"tenazas/internal/session"
)

// HandleCommand implements the `tenazas logs` subcommand.
func HandleCommand(sm *session.Manager, args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)

	typeFilter := fs.String("type", "", "Filter by audit type (llm_prompt, llm_response, cmd_result, status, etc.)")
	roleFilter := fs.String("role", "", "Filter by conversation role (user, assistant, system)")
	sinceStr := fs.String("since", "", "Show entries after this time (RFC3339 or HH:MM:SS)")
	untilStr := fs.String("until", "", "Show entries before this time (RFC3339 or HH:MM:SS)")
	search := fs.String("search", "", "Text search in log content")
	summary := fs.Bool("summary", false, "Show aggregated summary instead of full log")
	heartbeatName := fs.String("heartbeat", "", "Show logs for all sessions of a heartbeat")
	tail := fs.Int("tail", 0, "Show only the last N entries")
	follow := fs.Bool("follow", false, "Follow mode: watch for new entries")

	fs.Parse(args)

	f := &Filter{
		Type:      *typeFilter,
		Role:      *roleFilter,
		Search:    *search,
		Heartbeat: *heartbeatName,
	}

	if *sinceStr != "" {
		if t, err := parseTime(*sinceStr); err == nil {
			f.Since = t
		} else {
			fmt.Fprintf(os.Stderr, "Invalid --since format: %v\n", err)
			os.Exit(1)
		}
	}
	if *untilStr != "" {
		if t, err := parseTime(*untilStr); err == nil {
			f.Until = t
		} else {
			fmt.Fprintf(os.Stderr, "Invalid --until format: %v\n", err)
			os.Exit(1)
		}
	}

	if *heartbeatName != "" {
		handleHeartbeatLogs(sm, *heartbeatName, f, *summary)
		return
	}

	sessionID := fs.Arg(0)
	if sessionID == "" {
		// Default to latest session
		sess, err := sm.GetLatest()
		if err != nil {
			fmt.Fprintln(os.Stderr, "No session specified and no sessions found.")
			fmt.Fprintln(os.Stderr, "Usage: tenazas logs [session-id] [flags]")
			os.Exit(1)
		}
		sessionID = sess.ID
	}

	sess, err := sm.Load(sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Session not found: %s\n", sessionID)
		os.Exit(1)
	}

	auditPath := sm.AuditPath(sess)

	if *follow {
		handleFollow(auditPath, f)
		return
	}

	entries, err := ReadAuditFile(auditPath, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not read audit log: %v\n", err)
		os.Exit(1)
	}

	if *tail > 0 && len(entries) > *tail {
		entries = entries[len(entries)-*tail:]
	}

	if *summary {
		s := Summarize(entries, sess)
		fmt.Print(FormatSummary(s))
		return
	}

	for _, e := range entries {
		if e.Type == events.AuditLLMChunk {
			continue // skip chunks in full log view, they're noisy
		}
		fmt.Println(FormatEntry(e))
	}
}

func handleHeartbeatLogs(sm *session.Manager, hbName string, f *Filter, showSummary bool) {
	sessions, err := FindHeartbeatSessions(sm, hbName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error finding heartbeat sessions: %v\n", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Fprintf(os.Stderr, "No sessions found for heartbeat %q\n", hbName)
		os.Exit(1)
	}

	for _, sess := range sessions {
		auditPath := sm.AuditPath(sess)
		entries, err := ReadAuditFile(auditPath, f)
		if err != nil {
			continue
		}

		if showSummary {
			s := Summarize(entries, sess)
			fmt.Print(FormatSummary(s))
			fmt.Println("---")
		} else {
			fmt.Printf("\x1b[1m── Session %s (%s) ──\x1b[0m\n", sess.ID, sess.Status)
			for _, e := range entries {
				if e.Type == events.AuditLLMChunk {
					continue
				}
				fmt.Println(FormatEntry(e))
			}
			fmt.Println()
		}
	}
}

func handleFollow(path string, f *Filter) {
	entries, _ := ReadAuditFile(path, f)

	// Print the last few entries for context
	start := 0
	if len(entries) > 10 {
		start = len(entries) - 10
	}
	for _, e := range entries[start:] {
		if e.Type == events.AuditLLMChunk {
			continue
		}
		fmt.Println(FormatEntry(e))
	}

	lastCount := len(entries)
	fmt.Println("\x1b[2m── Following log (Ctrl+C to stop) ──\x1b[0m")

	for {
		time.Sleep(500 * time.Millisecond)
		current, err := ReadAuditFile(path, f)
		if err != nil {
			continue
		}
		if len(current) > lastCount {
			for _, e := range current[lastCount:] {
				if e.Type == events.AuditLLMChunk {
					continue
				}
				fmt.Println(FormatEntry(e))
			}
			lastCount = len(current)
		}
	}
}

func parseTime(s string) (time.Time, error) {
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Try HH:MM:SS (assume today)
	if t, err := time.Parse("15:04:05", s); err == nil {
		now := time.Now()
		return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), t.Second(), 0, now.Location()), nil
	}
	// Try date only
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339, HH:MM:SS, or YYYY-MM-DD format")
}
