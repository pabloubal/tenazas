package main

import (
	"testing"
)

func TestTelegramShouldDisplay(t *testing.T) {
	tg := &Telegram{}

	tests := []struct {
		verbosity string
		auditType string
		want      bool
	}{
		{"LOW", AuditIntervention, true},
		{"LOW", AuditStatus, true},
		{"LOW", AuditInfo, false},
		{"LOW", AuditLLMThought, false},
		{"MEDIUM", AuditInfo, true},
		{"MEDIUM", AuditLLMThought, false},
		{"HIGH", AuditLLMThought, false}, // This should fail if HIGH currently returns true for all
		{"HIGH", AuditLLMChunk, true},    // But AuditLLMChunk is handled separately in broadcastAudit
	}

	for _, tt := range tests {
		got := tg.shouldDisplay(tt.verbosity, tt.auditType)
		if got != tt.want {
			t.Errorf("shouldDisplay(%q, %q) = %v, want %v", tt.verbosity, tt.auditType, got, tt.want)
		}
	}
}
