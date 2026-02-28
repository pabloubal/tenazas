package formatter

import (
	"strings"
	"testing"

	"tenazas/internal/events"
)

func TestAnsiFormatter(t *testing.T) {
	f := &AnsiFormatter{}

	t.Run("SuccessCmdResult", func(t *testing.T) {
		e := events.AuditEntry{
			Type:    events.AuditCmdResult,
			Content: "Verification Result (Exit Code: 0):\x0aSuccess!",
		}
		out := f.Format(e)
		if !strings.Contains(out, "● Command Result") {
			t.Errorf("expected bullet-style command result, got %s", out)
		}
		if !strings.Contains(out, "└") {
			t.Errorf("expected └ summary prefix, got %s", out)
		}
	})

	t.Run("FailureCmdResult", func(t *testing.T) {
		e := events.AuditEntry{
			Type:     events.AuditCmdResult,
			Content:  "Verification Result (Exit Code: 1):\x0aError!",
			ExitCode: 1,
		}
		out := f.Format(e)
		if !strings.Contains(out, "● Command Result") {
			t.Errorf("expected bullet-style command result, got %s", out)
		}
		// Should contain red color for failure
		if !strings.Contains(out, "\x1b[31m") {
			t.Errorf("expected red color for failure, got %s", out)
		}
	})
}

func TestHtmlFormatter(t *testing.T) {
	f := &HtmlFormatter{}

	t.Run("SuccessCmdResult", func(t *testing.T) {
		e := events.AuditEntry{
			Type:    events.AuditCmdResult,
			Content: "Exit Code: 0\x0aOutput: all good",
		}
		out := f.Format(e)
		if !strings.Contains(out, "✅") {
			t.Errorf("expected success icon ✅ for Exit Code: 0, got %s", out)
		}
	})

	t.Run("FailureCmdResult", func(t *testing.T) {
		e := events.AuditEntry{
			Type:     events.AuditCmdResult,
			Content:  "Exit Code: 127\x0aOutput: command not found",
			ExitCode: 127,
		}
		out := f.Format(e)
		if !strings.Contains(out, "❌") {
			t.Errorf("expected failure icon ❌ for Exit Code: 127, got %s", out)
		}
	})
}
