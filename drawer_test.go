package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestAuditLLMThoughtConstant(t *testing.T) {
	// This test will fail to compile until Task 1 Step 1 is implemented
	if AuditLLMThought != "llm_thought" {
		t.Errorf("expected AuditLLMThought to be 'llm_thought', got %s", AuditLLMThought)
	}
}

func TestAnsiFormatter_Thought(t *testing.T) {
	f := &AnsiFormatter{}
	e := AuditEntry{
		Type:    AuditLLMThought,
		Content: "Thinking about the meaning of life",
	}
	out := f.Format(e)
	// Task 1 Step 3: Ensure thoughts have a basic format
	if !strings.Contains(out, "💭") {
		t.Errorf("expected thought icon 💭, got %s", out)
	}
	if !strings.Contains(out, "Thinking about the meaning of life") {
		t.Errorf("expected thought content, got %s", out)
	}
}

func TestCLI_DoubleTabToggle(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{Out: &out}

	// First tab
	cli.handleTab()
	if cli.IsImmersive {
		t.Error("expected IsImmersive to be false after first tab")
	}

	// Second tab within 300ms
	time.Sleep(50 * time.Millisecond)
	cli.handleTab()
	if !cli.IsImmersive {
		t.Error("expected IsImmersive to be true after double tab")
	}

	// Third tab after 300ms
	time.Sleep(400 * time.Millisecond)
	cli.handleTab()
	if !cli.IsImmersive {
		t.Error("expected IsImmersive to remain true after slow third tab")
	}

	// Fourth tab within 300ms of third
	time.Sleep(50 * time.Millisecond)
	cli.handleTab()
	if cli.IsImmersive {
		t.Error("expected IsImmersive to be false after second double tab")
	}
}

func TestCLI_AddThought(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{Out: &out}

	// Add 10 thoughts, should only keep last 8
	for i := 1; i <= 10; i++ {
		cli.addThought(string(rune('0' + i)))
	}

	if len(cli.drawer) != 8 {
		t.Errorf("expected drawer to have 8 lines, got %d", len(cli.drawer))
	}

	if cli.drawer[0] != "3" {
		t.Errorf("expected first line in drawer to be '3', got %s", cli.drawer[0])
	}
	if cli.drawer[7] != ":" { // '0'+10 is ':'
		t.Errorf("expected last line in drawer to be ':', got %s", cli.drawer[7])
	}
}

func TestCLI_DrawerRendering(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out:         &out,
		IsImmersive: true,
		drawer:      []string{"thought 1", "thought 2"},
	}

	// Mocking getTerminalSize is hard without refactoring,
	// but we can check if the escape sequences for drawing are present.
	cli.drawDrawer()

	output := out.String()
	// Check for ESC[ row;col H (move cursor) and ESC[ 2K (clear line)
	// and the thought content
	if !strings.Contains(output, "thought 1") {
		t.Error("expected output to contain 'thought 1'")
	}
	if !strings.Contains(output, "• thought 2") {
		t.Error("expected output to contain bulleted 'thought 2'")
	}
	if !strings.Contains(output, "\x1b[2K") {
		t.Error("expected output to contain line clear escape sequence")
	}
}

func TestCLI_SetupTerminalRegions(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}

	// Non-immersive: only 1 line reserved for footer
	cli.IsImmersive = false
	cli.setupTerminal()
	if !strings.Contains(out.String(), "\x1b[1;") {
		t.Error("expected scrolling region escape sequence")
	}

	out.Reset()
	// Immersive: 9 lines reserved
	cli.IsImmersive = true
	cli.setupTerminal()
	// We expect something like \x1b[1;<rows-9>r
	if !strings.Contains(out.String(), "\x1b[1;") {
		t.Error("expected scrolling region escape sequence in immersive mode")
	}
}

func TestCLI_ListenEvents_ThoughtRouting(t *testing.T) {
	// Task 5: Event Routing and Final Integration
	cli := &CLI{
		IsImmersive: true,
	}

	// The plan says listenEvents uses GlobalBus.Subscribe()
	// We can publish to it.

	// Start listening in background
	go cli.listenEvents("test-session")

	// Publish a thought event
	GlobalBus.Publish(Event{
		Type:      EventAudit,
		SessionID: "test-session",
		Payload: AuditEntry{
			Type:    AuditLLMThought,
			Content: "This should be routed",
		},
	})

	// Wait a bit and check if it reached the drawer
	// Note: We might need a small timeout or retry
	time.Sleep(100 * time.Millisecond)
	if len(cli.drawer) == 0 {
		t.Error("expected thought to be routed to drawer")
	}
	if cli.drawer[0] != "This should be routed" {
		t.Errorf("expected thought content 'This should be routed', got %s", cli.drawer[0])
	}
}

func TestCLI_ThreadSafeConcurrent(t *testing.T) {
	// Task 2: Implement Thread-Safe Writing
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}

	// This is Task 2: Implement Thread-Safe Writing
	// Just calling these methods concurrently should not panic or cause data races.
	// Note: We use -race flag in CI to actually catch races.
	for i := 0; i < 50; i++ {
		go cli.writeEscape("test")
		go cli.renderLine()
		go cli.addThought("test")
		go cli.drawDrawer()
	}
	// Give them some time to run
	time.Sleep(200 * time.Millisecond)
}
