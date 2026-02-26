package main

import (
	"fmt"
	"strings"
)

// ANSI Formatter for CLI
type AnsiFormatter struct{}

func (f *AnsiFormatter) Format(e AuditEntry) string {
	switch e.Type {
	case AuditInfo:
		return fmt.Sprintf("\x1b[34;1m🟦 %s\x1b[0m", e.Content)
	case AuditLLMPrompt:
		return fmt.Sprintf("\x1b[33m🟡 PROMPT (%s):\x1b[0m\x0a\x1b[90m%s\x1b[0m", e.Source, e.Content)
	case AuditLLMResponse:
		return fmt.Sprintf("\x1b[32;1m🟢 RESPONSE:\x1b[0m\x0a%s", e.Content)
	case AuditLLMThought:
		return fmt.Sprintf("\x1b[2m💭 %s\x1b[0m", e.Content)
	case AuditCmdResult:
		color, icon := "32", "✅"
		if !strings.Contains(e.Content, "Exit Code: 0") {
			color, icon = "31", "❌"
		}
		return fmt.Sprintf("\x1b[%s;1m%s COMMAND RESULT:\x1b[0m\x0a\x1b[90m%s\x1b[0m", color, icon, e.Content)
	case AuditIntervention:
		return fmt.Sprintf("\x1b[31;1m⚠️ INTERVENTION REQUIRED:\x1b[0m\x0a%s", e.Content)
	case AuditStatus:
		return fmt.Sprintf("\x1b[35;1m🟣 %s\x1b[0m", e.Content)
	default:
		return fmt.Sprintf("[%s] %s", e.Type, e.Content)
	}
}

// HTML Formatter for Telegram
type HtmlFormatter struct{}

func (f *HtmlFormatter) Format(e AuditEntry) string {
	content := f.escape(e.Content)
	switch e.Type {
	case AuditInfo:
		if strings.HasPrefix(e.Content, "Started") || strings.HasPrefix(e.Content, "Running") {
			return "🟦 <b>" + content + "</b>"
		}
		return "ℹ️ <i>" + content + "</i>"
	case AuditLLMPrompt:
		return "🟡 <b>PROMPT (" + e.Source + "):</b>\x0a<code>" + content + "</code>"
	case AuditLLMResponse:
		return "🟢 <b>RESPONSE:</b>\x0a" + content
	case AuditCmdResult:
		icon := "✅"
		if !strings.Contains(e.Content, "Exit Code: 0") {
			icon = "❌"
		}
		return icon + " <b>COMMAND RESULT:</b>\x0a<pre>" + content + "</pre>"
	case AuditIntervention:
		return "⚠️ <b>Intervention Required</b>\x0a" + content
	case AuditStatus:
		return "🟣 <b>" + content + "</b>"
	case AuditLLMThought:
		return "💭 <i>" + content + "</i>"
	default:
		return "<b>[" + e.Type + "]</b> " + content
	}
}

func (f *HtmlFormatter) escape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	// Markdown-ish to HTML
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
