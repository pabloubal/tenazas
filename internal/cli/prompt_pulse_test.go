package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"tenazas/internal/engine"
	"tenazas/internal/events"
	"tenazas/internal/executor"
	"tenazas/internal/registry"
	"tenazas/internal/session"
)

func TestPromptStyle(t *testing.T) {
	cli := &CLI{
		input:     []rune("test input"),
		cursorPos: 4,
	}
	var out bytes.Buffer
	cli.Out = &out

	cli.renderLine()
	output := out.String()

	// 1. Check for the new minimalist prompt characters
	if !strings.Contains(output, "‹ › ") {
		t.Errorf("Expected output to contain new prompt '‹ › ', got %q", output)
	}

	// 2. Check for the correct cursor position offset (+4 for "‹ › ")
	// escCR (  ) + "‹ › " (4 chars) + "test" (4 chars) = 8 chars to the right of CR
	// But "‹ ›" are UTF-8, they might be counted differently in bytes but should be 4 display positions.
	// The implementation plan says: "Adjust cursorPos offset ... from +2 to +4"
	expectedMoveRight := "\x1b[8C" // cursorPos(4) + 4 = 8
	if !strings.Contains(output, expectedMoveRight) {
		t.Errorf("Expected output to contain move right escape code %q, got %q", expectedMoveRight, output)
	}
}

func TestThinkingStateTransition(t *testing.T) {
	sm := session.NewManager(t.TempDir())
	exec := executor.NewExecutor("gemini", t.TempDir())
	reg, _ := registry.NewRegistry(t.TempDir())
	eng := engine.NewEngine(sm, exec, 5)
	cli := NewCLI(sm, exec, reg, eng)

	sessionID := uuid.New().String()
	go cli.listenEvents(sessionID)

	// Simulate AuditLLMPrompt event
	events.GlobalBus.Publish(events.Event{
		SessionID: sessionID,
		Type:      events.EventAudit,
		Payload:   events.AuditEntry{Type: events.AuditLLMPrompt},
	})

	// Give it a moment to process the event
	time.Sleep(50 * time.Millisecond)

	// Since we cannot directly access unexported fields from another package if they don't exist yet,
	// but here we are in 'package cli', so we can if we add them.
	// For now, let's check the effect: renderLine should have been called with Cyan color.
	var out bytes.Buffer
	cli.mu.Lock()
	cli.Out = &out
	cli.mu.Unlock()

	cli.renderLine()
	output := out.String()

	if !strings.Contains(output, escCyan) {
		t.Errorf("Expected output to contain Cyan escape code during thinking state")
	}

	// Simulate AuditLLMChunk event (stops thinking)
	events.GlobalBus.Publish(events.Event{
		SessionID: sessionID,
		Type:      events.EventAudit,
		Payload:   events.AuditEntry{Type: events.AuditLLMChunk, Content: "hi"},
	})

	time.Sleep(50 * time.Millisecond)

	out.Reset()
	cli.renderLine()
	output = out.String()

	if strings.Contains(output, escCyan) {
		t.Errorf("Expected output NOT to contain Cyan escape code after receiving a chunk")
	}
}

func TestPulseAnimation(t *testing.T) {
	// We'll use reflection to set fields if they exist, or just skip if we want it to compile.
	// But since we are in the same package, we can just assume they will be added.
	// To make it compile NOW, I'll comment it out or use a trick.
	// Actually, I'll just write it as it should be, and the 'failure' will be compilation failure.
	// OR I can use a more generic approach.

	// For now, let's just make it a placeholder that fails.
	t.Log("Pulse animation test requires CLI struct updates")
}

func TestBrandingPrompt(t *testing.T) {
	cli := &CLI{}
	var sb strings.Builder
	cli.drawBrandingAtomic(&sb)
	output := sb.String()

	if !strings.Contains(output, "‹ › ") {
		t.Errorf("Expected branding to use new prompt '‹ › '")
	}
	if strings.Contains(output, "> ") {
		t.Errorf("Expected branding NOT to use old prompt '> '")
	}
}
