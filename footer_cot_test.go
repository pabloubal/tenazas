package main

import (
	"testing"
)

func TestFormatFooter_CoT(t *testing.T) {
	cases := []struct {
		name       string
		mode       string
		yolo       bool
		skillCount int
		hint       string
		expected   string
	}{
		{
			name:       "Basic Plan mode with hint",
			mode:       ApprovalModePlan,
			yolo:       false,
			skillCount: 5,
			hint:       "Searching files...",
			expected:   "[PLAN] | Skills: 5 | Thought: Searching files...",
		},
		{
			name:       "Yolo mode with hint",
			mode:       ApprovalModePlan,
			yolo:       true,
			skillCount: 3,
			hint:       "Executing command",
			expected:   "[YOLO] | Skills: 3 | Thought: Executing command",
		},
		{
			name:       "Multi-line hint condensation",
			mode:       ApprovalModeAutoEdit,
			yolo:       false,
			skillCount: 10,
			hint:       "Searching...\nFound 5 files\nAnalyzing content",
			expected:   "[AUTO_EDIT] | Skills: 10 | Thought: Searching... Found 5 files Analyzing content",
		},
		{
			name:       "Excessive whitespace condensation",
			mode:       ApprovalModePlan,
			yolo:       false,
			skillCount: 1,
			hint:       "  Thinking    hard   ",
			expected:   "[PLAN] | Skills: 1 | Thought: Thinking hard",
		},
		{
			name:       "Empty hint",
			mode:       ApprovalModePlan,
			yolo:       false,
			skillCount: 0,
			hint:       "",
			expected:   "[PLAN] | Skills: 0 | Thought: ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// This call will fail to compile initially because formatFooter signature 
			// currently expects sessionID instead of hint.
			got := formatFooter(tc.mode, tc.yolo, tc.skillCount, tc.hint)
			if got != tc.expected {
				t.Errorf("formatFooter() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestCLILastThoughtState(t *testing.T) {
	cli := &CLI{}
	// This will fail to compile because lastThought field doesn't exist yet.
	cli.lastThought = "Initial thought"
	
	if cli.lastThought != "Initial thought" {
		t.Errorf("expected lastThought to be 'Initial thought', got %q", cli.lastThought)
	}
}
