package cli

import (
	"strings"
	"testing"

	"tenazas/internal/models"
)

func TestFormatFooter_CoT(t *testing.T) {
	cases := []struct {
		name     string
		data     FooterData
		contains []string
	}{
		{
			name:     "Basic Plan mode with hint",
			data:     FooterData{Mode: models.ApprovalModePlan, SkillCount: 5, Hint: "Searching files..."},
			contains: []string{"[PLAN]", "Skills: 5", "Thought: Searching files..."},
		},
		{
			name:     "Yolo mode with hint",
			data:     FooterData{Mode: models.ApprovalModePlan, Yolo: true, SkillCount: 3, Hint: "Executing command"},
			contains: []string{"[YOLO]", "Skills: 3", "Thought: Executing command"},
		},
		{
			name:     "Multi-line hint condensation",
			data:     FooterData{Mode: models.ApprovalModeAutoEdit, SkillCount: 10, Hint: "Searching...\nFound 5 files\nAnalyzing content"},
			contains: []string{"[AUTO_EDIT]", "Skills: 10", "Thought: Searching... Found 5 files Analyzing content"},
		},
		{
			name:     "Excessive whitespace condensation",
			data:     FooterData{Mode: models.ApprovalModePlan, SkillCount: 1, Hint: "  Thinking    hard   "},
			contains: []string{"[PLAN]", "Skills: 1", "Thought: Thinking hard"},
		},
		{
			name:     "Empty hint omits Thought",
			data:     FooterData{Mode: models.ApprovalModePlan, SkillCount: 0},
			contains: []string{"[PLAN]", "Skills: 0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatFooter(tc.data)
			for _, s := range tc.contains {
				if !strings.Contains(got, s) {
					t.Errorf("FormatFooter() = %q, missing %q", got, s)
				}
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
