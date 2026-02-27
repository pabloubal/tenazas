package main

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
	// However, we know drawerHeight is 8.

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

	// r2Imm should be r2Non - (drawerHeight + 1)
	// Because non-immersive reserves 1, immersive should reserve drawerHeight + 2.
	// Difference is drawerHeight + 1 = 9.
	expectedDiff := drawerHeight + 1
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
	// rows - drawerHeight - 1
	rows, _, err := getTerminalSize()
	if err != nil {
		rows = 24
	}
	expectedRow := rows - drawerHeight - 1
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

	// drawDrawer should draw at rows - drawerHeight + i
	rows, _, err := getTerminalSize()
	if err != nil {
		rows = 24
	}

	// First thought should be at rows - drawerHeight (e.g. 24 - 8 = 16)
	expectedRow := rows - drawerHeight
	expectedMove := fmt.Sprintf("\x1b[%d;1H", expectedRow)

	if !strings.Contains(output, expectedMove) {
		t.Errorf("expected drawer to start at row %d, sequence %q not found in %q", expectedRow, expectedMove, output)
	}
}
