package telegram

import (
	"testing"

	"tenazas/internal/events"
)

func TestTelegramShouldDisplay(t *testing.T) {
	tg := &Telegram{}

	tests := []struct {
		verbosity string
		auditType string
		want      bool
	}{
		{"LOW", events.AuditIntervention, true},
		{"LOW", events.AuditStatus, true},
		{"LOW", events.AuditInfo, false},
		{"LOW", events.AuditLLMThought, false},
		{"MEDIUM", events.AuditInfo, true},
		{"MEDIUM", events.AuditLLMThought, false},
		{"HIGH", events.AuditLLMThought, false}, // This should fail if HIGH currently returns true for all
		{"HIGH", events.AuditLLMChunk, true},    // But AuditLLMChunk is handled separately in broadcastAudit
	}

	for _, tt := range tests {
		got := tg.shouldDisplay(tt.verbosity, tt.auditType)
		if got != tt.want {
			t.Errorf("shouldDisplay(%q, %q) = %v, want %v", tt.verbosity, tt.auditType, got, tt.want)
		}
	}
}
