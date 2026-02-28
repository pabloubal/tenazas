package cli

import (
	"strings"
	"testing"

	"tenazas/internal/models"
)

func TestFormatFooter_CoT(t *testing.T) {
	cols := 100
	cases := []struct {
		name     string
		data     FooterData
		line2has []string
	}{
		{
			name:     "Basic Plan mode",
			data:     FooterData{Mode: models.ApprovalModePlan, SkillCount: 5},
			line2has: []string{"PLAN", "Skills: 5"},
		},
		{
			name:     "Yolo mode",
			data:     FooterData{Mode: models.ApprovalModePlan, Yolo: true, SkillCount: 3},
			line2has: []string{"YOLO", "Skills: 3"},
		},
		{
			name:     "AutoEdit mode",
			data:     FooterData{Mode: models.ApprovalModeAutoEdit, SkillCount: 10},
			line2has: []string{"AUTO_EDIT", "Skills: 10"},
		},
		{
			name:     "Empty mode defaults to PLAN",
			data:     FooterData{Mode: "", SkillCount: 0},
			line2has: []string{"PLAN", "Skills: 0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatFooterLine2(tc.data, cols)
			for _, s := range tc.line2has {
				if !strings.Contains(got, s) {
					t.Errorf("FormatFooterLine2() = %q, missing %q", got, s)
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
