package telegram

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractShellCommand(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "Bash block",
			content:  "Here is the command:\n" + "```bash\nls -la\n```" + "\nLet me know if it works.",
			expected: "ls -la",
		},
		{
			name:     "Sh block",
			content:  "Try this:\n" + "```sh\necho 'hello world'\n```",
			expected: "echo 'hello world'",
		},
		{
			name:     "Shell block",
			content:  "```shell\nmkdir -p test/dir\n```",
			expected: "mkdir -p test/dir",
		},
		{
			name:     "No language block",
			content:  "Run this:\n```\ncat file.txt\n```",
			expected: "cat file.txt",
		},
		{
			name:     "Multiple blocks - picks first",
			content:  "First:\n" + "```bash\ncd src\n```" + "\nSecond:\n" + "```bash\nls\n```",
			expected: "cd src",
		},
		{
			name:     "No blocks",
			content:  "Just some text with no commands.",
			expected: "",
		},
		{
			name:     "Empty block",
			content:  "```bash\n\n```",
			expected: "",
		},
		{
			name:     "Inline code - should be ignored",
			content:  "Use `ls` to see files.",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractShellCommand(tt.content)
			if got != tt.expected {
				t.Errorf("ExtractShellCommand() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestGetActionKeyboard(t *testing.T) {
	sessionID := "test-session-uuid"

	t.Run("Without command", func(t *testing.T) {
		content := "Hello, how can I help you today?"
		markupRaw := getActionKeyboard(sessionID, content)

		// Convert to JSON to easily inspect structure
		data, err := json.Marshal(markupRaw)
		if err != nil {
			t.Fatalf("Failed to marshal markup: %v", err)
		}

		js := string(data)

		// Check for mandatory buttons
		if !strings.Contains(js, "Continue") || !strings.Contains(js, "act:continue_prompt:"+sessionID) {
			t.Errorf("Missing Continue button or correct callback")
		}
		if !strings.Contains(js, "New Session") || !strings.Contains(js, "act:new_session:"+sessionID) {
			t.Errorf("Missing New Session button or correct callback")
		}
		if !strings.Contains(js, "More Actions...") || !strings.Contains(js, "act:more_actions:"+sessionID) {
			t.Errorf("Missing More Actions button or correct callback")
		}

		// Should NOT have Run button
		if strings.Contains(js, "Run:") {
			t.Errorf("Should not have Run button when no command is present")
		}
	})

	t.Run("With command", func(t *testing.T) {
		content := "I recommend running:\n" + "```bash\ngit status\n```"
		markupRaw := getActionKeyboard(sessionID, content)

		data, err := json.Marshal(markupRaw)
		if err != nil {
			t.Fatalf("Failed to marshal markup: %v", err)
		}

		js := string(data)

		// Should have Run button
		if !strings.Contains(js, "Run: git status") || !strings.Contains(js, "act:run_command:"+sessionID) {
			t.Errorf("Missing Run button or correct callback. Got: %s", js)
		}
	})

	t.Run("With long command - shortened", func(t *testing.T) {
		content := "```bash\ndocker run -it --rm -v $(pwd):/app node:18 npm install && npm run build\n```"
		markupRaw := getActionKeyboard(sessionID, content)

		data, _ := json.Marshal(markupRaw)
		js := string(data)

		// Check that it's present but likely shortened (assuming ~20 chars as per plan)
		if !strings.Contains(js, "Run:") {
			t.Errorf("Missing Run button")
		}

		// Simple check for shortening - shouldn't contain the full long string in the button text
		if strings.Contains(js, "Run: docker run -it --rm -v $(pwd):/app node:18 npm install && npm run build") {
			t.Errorf("Command button text was not shortened")
		}
	})
}
