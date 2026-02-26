package main

import (
	"testing"
)

func TestThoughtParserComprehensive(t *testing.T) {
	tests := []struct {
		name             string
		chunks           []string
		expectedText     string
		expectedThoughts string
	}{
		{
			name:             "Plain text",
			chunks:           []string{"Hello world"},
			expectedText:     "Hello world",
			expectedThoughts: "",
		},
		{
			name:             "Simple thought",
			chunks:           []string{"<thought>thinking</thought>Action"},
			expectedText:     "Action",
			expectedThoughts: "thinking",
		},
		{
			name:             "Split tag across chunks",
			chunks:           []string{"<tho", "ught>internal</thou", "ght> done"},
			expectedText:     " done",
			expectedThoughts: "internal",
		},
		{
			name:             "Multiple thought blocks",
			chunks:           []string{"<thought>1</thought>", " and ", "<thought>2</thought>"},
			expectedText:     " and ",
			expectedThoughts: "12",
		},
		{
			name:             "Data containing less-than but not a tag",
			chunks:           []string{"Value < 100 is true"},
			expectedText:     "Value < 100 is true",
			expectedThoughts: "",
		},
		{
			name:             "Partial tag prefix that fails later",
			chunks:           []string{"<th", " is not what you think"},
			expectedText:     "<th is not what you think",
			expectedThoughts: "",
		},
		{
			name:             "Tag split at the very beginning",
			chunks:           []string{"<", "thought>test</thought>"},
			expectedText:     "",
			expectedThoughts: "test",
		},
		{
			name:             "Tag split at the very end",
			chunks:           []string{"<thought>test</thou", "ght>"},
			expectedText:     "",
			expectedThoughts: "test",
		},
		{
			name:             "Empty thought block",
			chunks:           []string{"Start <thought></thought> End"},
			expectedText:     "Start  End",
			expectedThoughts: "",
		},
		{
			name:             "Incomplete thought block (Bug candidate)",
			chunks:           []string{"Started <thought>thinking"},
			expectedText:     "Started ",
			expectedThoughts: "thinking",
		},
		{
			name:             "Unclosed tag at end of stream (Bug candidate)",
			chunks:           []string{"Final <"},
			expectedText:     "Final <",
			expectedThoughts: "",
		},
		{
			name:             "Nested-looking tags (non-recursive)",
			chunks:           []string{"<thought> A <thought> B </thought> C </thought>"},
			expectedText:     " C </thought>",
			expectedThoughts: " A <thought> B ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotText, gotThoughts string
			parser := &thoughtParser{
				onThought: func(s string) { gotThoughts += s },
				onText:    func(s string) { gotText += s },
			}

			for _, chunk := range tt.chunks {
				parser.parse(chunk)
			}
			parser.parse("")

			if gotText != tt.expectedText {
				t.Errorf("%s - text: got %q, want %q", tt.name, gotText, tt.expectedText)
			}
			if gotThoughts != tt.expectedThoughts {
				t.Errorf("%s - thoughts: got %q, want %q", tt.name, gotThoughts, tt.expectedThoughts)
			}
		})
	}
}
