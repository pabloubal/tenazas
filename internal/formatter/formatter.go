package formatter

import (
	"fmt"
	"strings"

	"tenazas/internal/events"
)

// AnsiFormatter renders audit entries for terminal output.
type AnsiFormatter struct{}

func (f *AnsiFormatter) Format(e events.AuditEntry) string {
	switch e.Type {
	case events.AuditInfo:
		return fmt.Sprintf("\x1b[32m● \x1b[0m%s", e.Content)
	case events.AuditLLMPrompt:
		return fmt.Sprintf("\x1b[2m● Thinking...\x1b[0m")
	case events.AuditLLMResponse:
		return fmt.Sprintf("\x1b[32m● Response:\x1b[0m\n%s", e.Content)
	case events.AuditLLMThought:
		return fmt.Sprintf("\x1b[2m  %s\x1b[0m", e.Content)
	case events.AuditCmdResult:
		color, icon := "\x1b[32m", "●"
		if !strings.Contains(e.Content, "Exit Code: 0") {
			color, icon = "\x1b[31m", "●"
		}
		return fmt.Sprintf("%s%s Command Result\x1b[0m\n\x1b[2m  └ %s\x1b[0m", color, icon, e.Content)
	case events.AuditIntervention:
		return fmt.Sprintf("\x1b[31;1m● Intervention Required\x1b[0m\n  %s", e.Content)
	case events.AuditStatus:
		return fmt.Sprintf("\x1b[35m● %s\x1b[0m", e.Content)
	default:
		return fmt.Sprintf("● [%s] %s", e.Type, e.Content)
	}
}

// HtmlFormatter renders audit entries for Telegram HTML output.
type HtmlFormatter struct{}

func (f *HtmlFormatter) Format(e events.AuditEntry) string {
	content := f.Escape(e.Content)
	switch e.Type {
	case events.AuditInfo:
		if strings.HasPrefix(e.Content, "Started") || strings.HasPrefix(e.Content, "Running") {
			return "🟦 <b>" + content + "</b>"
		}
		return "ℹ️ <i>" + content + "</i>"
	case events.AuditLLMPrompt:
		return "🟡 <b>PROMPT (" + e.Source + "):</b>\n<code>" + content + "</code>"
	case events.AuditLLMResponse:
		return "🟢 <b>RESPONSE:</b>\n" + content
	case events.AuditCmdResult:
		icon := "✅"
		if !strings.Contains(e.Content, "Exit Code: 0") {
			icon = "❌"
		}
		return icon + " <b>COMMAND RESULT:</b>\n<pre>" + content + "</pre>"
	case events.AuditIntervention:
		return "⚠️ <b>Intervention Required</b>\n" + content
	case events.AuditStatus:
		return "🟣 <b>" + content + "</b>"
	case events.AuditLLMThought:
		return "💭 <i>" + content + "</i>"
	default:
		return "<b>[" + e.Type + "]</b> " + content
	}
}

func (f *HtmlFormatter) Escape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	for strings.Count(s, "**") >= 2 {
		s = strings.Replace(s, "**", "<b>", 1)
		s = strings.Replace(s, "**", "</b>", 1)
	}
	for strings.Count(s, "```") >= 2 {
		s = strings.Replace(s, "```", "<pre>", 1)
		s = strings.Replace(s, "```", "</pre>", 1)
	}
	for strings.Count(s, "`") >= 2 {
		s = strings.Replace(s, "`", "<code>", 1)
		s = strings.Replace(s, "`", "</code>", 1)
	}
	if len(s) > 3500 {
		s = s[:3500] + "...[TRUNCATED]"
	}
	return s
}
