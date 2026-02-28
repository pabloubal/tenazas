package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tenazas/internal/session"
)

func TestRenderLine(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out:       &out,
		input:     []rune("/run"),
		cursorPos: 4,
	}

	// Mocking suggestion for TestRenderLine
	cli.completions = []string{"/run deploy"}

	cli.renderLine()
	output := out.String()

	// Should contain the input and the dimmed suggestion
	if !strings.Contains(output, "/run") {
		t.Errorf("renderLine output should contain input /run, got %q", output)
	}
	if !strings.Contains(output, "deploy") {
		t.Errorf("renderLine output should contain dimmed suggestion deploy, got %q", output)
	}
	// Check for cursor movement and clearing ANSI codes
	if !strings.Contains(output, "\r") {
		t.Errorf("renderLine output should contain carriage return, got %q", output)
	}
	if !strings.Contains(output, "\x1b[K") {
		t.Errorf("renderLine output should contain clear to end of line, got %q", output)
	}
}

func TestHandleHelp(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{Out: &out}

	cli.handleHelp()
	output := out.String()

	expectedCommands := []string{"/run", "/last", "/intervene", "/skills", "/mode", "/budget", "/help"}
	for _, cmd := range expectedCommands {
		if !strings.Contains(output, cmd) {
			t.Errorf("handleHelp output should contain command %q, got %q", cmd, output)
		}
	}

	expectedModes := []string{"plan", "auto_edit", "yolo"}
	for _, mode := range expectedModes {
		if !strings.Contains(output, mode) {
			t.Errorf("handleHelp output should contain mode %q, got %q", mode, output)
		}
	}
}

func TestGetCompletions(t *testing.T) {
	cli := &CLI{}

	tests := []struct {
		input    string
		expected []string
	}{
		{"/", []string{"/run", "/last", "/intervene", "/skills", "/mode", "/tier", "/budget", "/tasks", "/task", "/help"}},
		{"/r", []string{"/run"}},
		{"/l", []string{"/last"}},
		{"/i", []string{"/intervene"}},
		{"/s", []string{"/skills"}},
		{"/m", []string{"/mode"}},
		{"/t", []string{"/tier", "/tasks", "/task"}},
		{"/b", []string{"/budget"}},
		{"/h", []string{"/help"}},
		{"/notfound", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := cli.getCompletions(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getCompletions(%q) = %v; want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetSkillCompletions(t *testing.T) {
	// Mock skills directory
	tmpDir := t.TempDir()
	sm := session.NewManager(tmpDir)
	cli := &CLI{Sm: sm}

	// Create some mock skills
	skills := []string{"deploy", "test", "build"}
	for _, s := range skills {
		os.MkdirAll(filepath.Join(tmpDir, "skills", s), 0755)
		os.WriteFile(filepath.Join(tmpDir, "skills", s, "skill.json"), []byte("{}"), 0644)
	}

	tests := []struct {
		input    string
		expected []string
	}{
		{"/run ", []string{"/run build", "/run deploy", "/run test"}},
		{"/run d", []string{"/run deploy"}},
		{"/run b", []string{"/run build"}},
		{"/run t", []string{"/run test"}},
		{"/run x", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := cli.getCompletions(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("getCompletions(%q) = %v; want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetDimmedSuggestion(t *testing.T) {
	cli := &CLI{}

	tests := []struct {
		input    string
		expected string
	}{
		{"/r", "un"},
		{"/ru", "n"},
		{"/run", ""},
		{"/", ""}, // More than one match
		{"/l", "ast"},
		{"/run d", "eploy"}, // Assuming "deploy" is the only match starting with d
	}

	// Mocking completions for the last case
	cli.completions = []string{"/run deploy"}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if tt.input == "/run d" {
				cli.completions = []string{"/run deploy"}
			} else {
				cli.completions = cli.getCompletions(tt.input)
			}
			got := cli.getDimmedSuggestion(tt.input)
			if got != tt.expected {
				t.Errorf("getDimmedSuggestion(%q) = %q; want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestHandleTab(t *testing.T) {
	cli := &CLI{
		input: []rune("/r"),
	}

	// First TAB: Completes to /run
	cli.handleTab()
	if string(cli.input) != "/run" {
		t.Errorf("first TAB: expected %q, got %q", "/run", string(cli.input))
	}

	// Multiple matches
	cli.input = []rune("/s")
	cli.completions = []string{"/skills", "/stop"} // /stop doesn't exist but for testing cycle
	cli.completionIdx = -1

	cli.handleTab()
	if string(cli.input) != "/skills" {
		t.Errorf("cycle 1: expected %q, got %q", "/skills", string(cli.input))
	}

	cli.handleTab()
	if string(cli.input) != "/stop" {
		t.Errorf("cycle 2: expected %q, got %q", "/stop", string(cli.input))
	}

	cli.handleTab()
	if string(cli.input) != "/skills" {
		t.Errorf("cycle 3: expected %q, got %q", "/skills", string(cli.input))
	}
}

func TestInputBufferManipulation(t *testing.T) {
	cli := &CLI{}

	// Insert characters
	cli.handleRune('a')
	cli.handleRune('b')
	cli.handleRune('c')
	if string(cli.input) != "abc" {
		t.Errorf("expected %q, got %q", "abc", string(cli.input))
	}
	if cli.cursorPos != 3 {
		t.Errorf("expected cursorPos 3, got %d", cli.cursorPos)
	}

	// Move cursor back (manually for now as we don't have handleLeft yet)
	cli.cursorPos = 1 // Between 'a' and 'b'

	// Insert mid-line
	cli.handleRune('x')
	if string(cli.input) != "axbc" {
		t.Errorf("mid-line insert: expected %q, got %q", "axbc", string(cli.input))
	}
	if cli.cursorPos != 2 {
		t.Errorf("expected cursorPos 2, got %d", cli.cursorPos)
	}

	// Backspace mid-line
	cli.handleBackspace()
	if string(cli.input) != "abc" {
		t.Errorf("mid-line backspace: expected %q, got %q", "abc", string(cli.input))
	}
	if cli.cursorPos != 1 {
		t.Errorf("expected cursorPos 1, got %d", cli.cursorPos)
	}

	// Backspace at 0
	cli.cursorPos = 0
	cli.handleBackspace()
	if string(cli.input) != "abc" {
		t.Errorf("backspace at 0: expected %q, got %q", "abc", string(cli.input))
	}
	if cli.cursorPos != 0 {
		t.Errorf("expected cursorPos 0, got %d", cli.cursorPos)
	}
}
