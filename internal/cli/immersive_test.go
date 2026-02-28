package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestImmersive_SetupTerminalRegions(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}

	// We can't easily mock getTerminalSize here, but we can check the relative difference
	// or assume a default if we can't get it.
	// However, we know DrawerHeight is 8.

	// Non-immersive: only 1 line reserved for footer
	cli.IsImmersive = false
	cli.setupTerminal()
	outputNon := out.String()

	out.Reset()
	// Immersive: 10 lines reserved (Footer + Drawer + Fixed Prompt)
	cli.IsImmersive = true
	cli.setupTerminal()
	outputImm := out.String()

	// Parse the scrolling region escape sequence: \x1b[%d;%dr
	var r1Non, r2Non int
	fmt.Sscanf(strings.TrimPrefix(outputNon, "\x1b["), "%d;%dr", &r1Non, &r2Non)

	var r1Imm, r2Imm int
	fmt.Sscanf(strings.TrimPrefix(outputImm, "\x1b["), "%d;%dr", &r1Imm, &r2Imm)

	// r2Imm should be r2Non - (DrawerHeight + 1)
	// Because non-immersive reserves 1, immersive should reserve DrawerHeight + 2.
	// Difference is DrawerHeight + 1 = 9.
	expectedDiff := DrawerHeight + 1
	actualDiff := r2Non - r2Imm

	if actualDiff != expectedDiff {
		t.Errorf("expected immersive mode to reserve %d more lines than non-immersive, got %d (non: %d, imm: %d)", expectedDiff, actualDiff, r2Non, r2Imm)
	}
}

func TestImmersive_PromptPositioning(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out:         &out,
		IsImmersive: true,
	}

	// In immersive mode, renderLine should move the cursor to a fixed position.
	cli.renderLine()
	output := out.String()

	// Check for escMoveTo: \x1b[%d;1H
	// rows - DrawerHeight - 1
	rows, _, err := getTerminalSize()
	if err != nil {
		rows = 24
	}
	// rows - 2 (prompt is always at rows-2 in the box layout)
	expectedRow := rows - 2
	expectedMove := fmt.Sprintf("\x1b[%d;1H", expectedRow)

	if !strings.Contains(output, expectedMove) {
		t.Errorf("expected output to contain prompt positioning sequence %q, got %q", expectedMove, output)
	}
}

func TestImmersive_DrawerBoundary(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out:         &out,
		IsImmersive: true,
		drawer:      []string{"test thought"},
	}

	cli.drawDrawer()
	output := out.String()

	// drawDrawer should draw at rows - DrawerHeight + i
	rows, _, err := getTerminalSize()
	if err != nil {
		rows = 24
	}

	// First thought should be at rows - DrawerHeight - 4 (drawer sits above footer line 1)
	expectedRow := rows - DrawerHeight - 4
	expectedMove := fmt.Sprintf("\x1b[%d;1H", expectedRow)

	if !strings.Contains(output, expectedMove) {
		t.Errorf("expected drawer to start at row %d, sequence %q not found in %q", expectedRow, expectedMove, output)
	}
}
