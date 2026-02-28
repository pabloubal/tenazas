package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"path/filepath"

	"tenazas/internal/client"
	"tenazas/internal/engine"
	"tenazas/internal/models"
	"tenazas/internal/session"
)

func TestFormatFooter(t *testing.T) {
	cols := 100

	// Test line 1: path + branch + client + tier
	d := FooterData{
		Mode: models.ApprovalModePlan, SkillCount: 5, CWD: "/tmp/project",
		GitBranch: "main", ClientName: "gemini", ModelTier: "high",
	}
	got1 := FormatFooterLine1(d, cols)
	if !strings.Contains(got1, "/tmp/project") {
		t.Errorf("Line1 missing CWD, got %q", got1)
	}
	if !strings.Contains(got1, "[main]") {
		t.Errorf("Line1 missing git branch, got %q", got1)
	}
	if !strings.Contains(got1, "gemini (high)") {
		t.Errorf("Line1 missing client+tier, got %q", got1)
	}

	// Test line 2: mode + keybinding hints + skills
	got2 := FormatFooterLine2(d, cols)
	if !strings.Contains(got2, "shift+tab PLAN") {
		t.Errorf("Line2 missing mode hint, got %q", got2)
	}
	if !strings.Contains(got2, "Skills: 5") {
		t.Errorf("Line2 missing skill count, got %q", got2)
	}

	// Yolo mode
	d2 := FooterData{Mode: models.ApprovalModePlan, Yolo: true, SkillCount: 3, MaxBudgetUSD: 5.50, ClientName: "claude-code"}
	got2y := FormatFooterLine2(d2, cols)
	if !strings.Contains(got2y, "shift+tab YOLO") {
		t.Errorf("Line2 missing YOLO mode, got %q", got2y)
	}
	got1y := FormatFooterLine1(d2, cols)
	if !strings.Contains(got1y, "$5.50") {
		t.Errorf("Line1 missing budget, got %q", got1y)
	}

	// Budget should NOT appear when 0
	d3 := FooterData{Mode: "PLAN", ClientName: "gemini"}
	got1z := FormatFooterLine1(d3, cols)
	if strings.Contains(got1z, "$") {
		t.Errorf("expected no Budget in footer when 0, got %q", got1z)
	}

	// Empty mode defaults to PLAN
	d4 := FooterData{Mode: "", SkillCount: 0}
	got2z := FormatFooterLine2(d4, cols)
	if !strings.Contains(got2z, "PLAN") {
		t.Errorf("expected default PLAN mode, got %q", got2z)
	}
}

func TestGetTerminalSize(t *testing.T) {
	// This will fail to compile if getTerminalSize is not defined
	rows, cols, err := getTerminalSize()
	if err != nil {
		t.Logf("getTerminalSize returned error (expected in non-TTY): %v", err)
	} else {
		if rows <= 0 || cols <= 0 {
			t.Errorf("got non-positive terminal size: rows=%d, cols=%d", rows, cols)
		}
	}
}

func TestModeCommand(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}
	sess := &models.Session{
		ID:          uuid.New().String(),
		LastUpdated: time.Now(),
		// ApprovalMode will fail to compile if not added to Session struct
		ApprovalMode: models.ApprovalModePlan,
	}

	// This will fail to compile if handleMode is not defined
	cli.handleMode(sess, []string{"auto_edit"})
	if sess.ApprovalMode != models.ApprovalModeAutoEdit {
		t.Errorf("expected mode AUTO_EDIT, got %s", sess.ApprovalMode)
	}

	cli.handleMode(sess, []string{"yolo"})
	if !sess.Yolo {
		t.Errorf("expected Yolo to be true")
	}

	cli.handleMode(sess, []string{"plan"})
	if sess.ApprovalMode != models.ApprovalModePlan || sess.Yolo {
		t.Errorf("expected mode PLAN and Yolo false, got %s, %v", sess.ApprovalMode, sess.Yolo)
	}
}

func TestDrawFooterSequences(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}
	sess := &models.Session{
		ID:           "1234567890abcdef",
		ApprovalMode: models.ApprovalModePlan,
	}

	cli.lastThought = "Testing footer"
	cli.skillCount = 5

	cli.drawFooter(sess)

	got := out.String()

	if !strings.Contains(got, "\x1b[s") {
		t.Errorf("output missing save cursor sequence")
	}
	if !strings.Contains(got, "\x1b[u") {
		t.Errorf("output missing restore cursor sequence")
	}
	if !strings.Contains(got, "PLAN") {
		t.Errorf("output missing mode PLAN")
	}
	if !strings.Contains(got, "Skills: 5") {
		t.Errorf("output missing skill count")
	}
}

func TestSetupTerminal(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{
		Out: &out,
	}

	// This will fail to compile if setupTerminal is not defined
	cli.setupTerminal()

	got := out.String()
	// Check for scrolling region sequence: \033[1;<N-1>r
	// Since N depends on the actual terminal, we check for the general pattern.
	if !strings.Contains(got, "\x1b[1;") || !strings.Contains(got, "r") {
		t.Errorf("output missing scrolling region sequence")
	}
}

func TestEngineRespectsSessionApprovalMode(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-mode-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	c, _ := client.NewClient("echo", "echo", filepath.Join(storageDir, "tenazas.log"))
	clients := map[string]client.Client{"echo": c}
	eng := engine.NewEngine(sm, clients, "echo", 5)
	_ = eng

	sess := &models.Session{
		ID:           "sess-mode",
		CWD:          storageDir,
		ApprovalMode: models.ApprovalModeAutoEdit,
		RoleCache:    make(map[string]string),
	}
	sm.Save(sess)

	// This test is mostly to ensure the field exists and is accessible by the engine logic.
	// A full behavioral test would require mocking the Gemini CLI to see if --approval-mode was passed.
	if sess.ApprovalMode != models.ApprovalModeAutoEdit {
		t.Errorf("expected AUTO_EDIT, got %s", sess.ApprovalMode)
	}
}

func TestCLIInitializeSession(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-cli-init-*")
	defer os.RemoveAll(tmpDir)

	sm := session.NewManager(tmpDir)
	cli := &CLI{
		Sm:               sm,
		DefaultModelTier: "high",
	}

	sess, err := cli.initializeSession(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sess.ApprovalMode != models.ApprovalModePlan {
		t.Errorf("expected default ApprovalMode 'PLAN', got %s", sess.ApprovalMode)
	}
	if sess.ModelTier != "high" {
		t.Errorf("expected default ModelTier 'high', got %s", sess.ModelTier)
	}
}

func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

func TestDrawFooterStepPrefix(t *testing.T) {
	var out bytes.Buffer
	cli := &CLI{Out: &out}
	sess := &models.Session{
		ID:           "step-test",
		ApprovalMode: models.ApprovalModePlan,
		SkillName:    "my-skill",
		ActiveNode:   "validate",
	}
	cli.currentTask = "Running tests"
	cli.drawFooter(sess)

	// Strip ANSI escape sequences for matching (shimmer wraps each char in color codes)
	got := out.String()
	plain := stripANSI(got)
	if !strings.Contains(plain, "Step validate: Running tests") {
		t.Errorf("expected step prefix in shimmer, plain text: %q", plain)
	}
}
