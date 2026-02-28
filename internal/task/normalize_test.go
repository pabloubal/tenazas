package task

import "testing"

func TestNormalizeTaskIDExported(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"6", "TSK-000006"},
		{"tsk-6", "TSK-6"},      // uppercase prefix variant
		{"TSK-000006", "TSK-000006"}, // already canonical
		{"42", "TSK-000042"},
		{"1", "TSK-000001"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeTaskID(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeTaskID(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
