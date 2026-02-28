package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"tenazas/internal/client"
	_ "tenazas/internal/client" // register clients
	"tenazas/internal/events"
	"tenazas/internal/models"
	"tenazas/internal/session"
)

func newTestClient(binPath, storageDir string) map[string]client.Client {
	c, _ := client.NewClient("gemini", binPath, filepath.Join(storageDir, "tenazas.log"))
	return map[string]client.Client{"gemini": c}
}

func TestEngineBasic(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-test-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	engine := NewEngine(sm, newTestClient("echo", storageDir), "gemini", 5)

	skill := &models.SkillGraph{
		Name:         "test-skill",
		InitialState: "start",
		States: map[string]models.StateDef{
			"start": {
				Type: "end",
			},
		},
	}

	sess := &models.Session{
		ID:        "sess-1",
		CWD:       ".",
		SkillName: "test-skill",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	engine.Run(skill, sess)

	if sess.Status != "completed" {
		t.Errorf("expected status 'completed', got %s", sess.Status)
	}
}

func TestEngineTool(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-tool-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	engine := NewEngine(sm, newTestClient("echo", storageDir), "gemini", 5)

	skill := &models.SkillGraph{
		Name:         "tool-skill",
		InitialState: "run-tool",
		States: map[string]models.StateDef{
			"run-tool": {
				Type:    "tool",
				Command: "ls",
				Next:    "finish",
			},
			"finish": {
				Type: "end",
			},
		},
	}

	sess := &models.Session{
		ID:        "sess-tool",
		CWD:       ".",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	engine.Run(skill, sess)

	if sess.Status != "completed" {
		t.Errorf("expected 'completed', got %s", sess.Status)
	}
}

func TestEngineIntervention(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-interv-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	engine := NewEngine(sm, newTestClient("echo", storageDir), "gemini", 5)

	skill := &models.SkillGraph{
		Name:         "interv-skill",
		InitialState: "wait",
		States: map[string]models.StateDef{
			"wait": {
				Type: "end",
			},
		},
	}

	sess := &models.Session{
		ID:         "sess-interv",
		Status:     "intervention_required",
		ActiveNode: "wait",
		RoleCache:  make(map[string]string),
	}
	sm.Save(sess)

	// Resolve intervention in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		engine.ResolveIntervention("sess-interv", "retry")
	}()

	engine.Run(skill, sess)

	if sess.Status != "completed" {
		t.Errorf("expected 'completed' after intervention, got %s", sess.Status)
	}
}

func TestEngineActionLoop(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-loop-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)

	// Create a dummy binary that we can use for executor
	dummyScript := `#!/bin/bash
echo '{"type": "init", "session_id": "sid-999"}'
echo '{"type": "message", "content": "Done"}'
`
	scriptPath := filepath.Join(storageDir, "dummy.sh")
	os.WriteFile(scriptPath, []byte(dummyScript), 0755)

	exec := newTestClient(scriptPath, storageDir)
	engine := NewEngine(sm, exec, "gemini", 2)

	skill := &models.SkillGraph{
		Name:         "loop-skill",
		InitialState: "step1",
		States: map[string]models.StateDef{
			"step1": {
				Type:         "action_loop",
				SessionRole:  "worker",
				Instruction:  "do something",
				PreActionCmd: "echo 'pre'",
				VerifyCmd:    "ls " + scriptPath, // Always succeeds
				Next:         "finish",
			},
			"finish": {
				Type: "end",
			},
		},
	}

	sess := &models.Session{
		ID:        "sess-loop",
		CWD:       ".",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	engine.Run(skill, sess)

	if sess.Status != "completed" {
		t.Errorf("expected 'completed', got %s", sess.Status)
	}
	if sess.RoleCache["worker"] != "sid-999" {
		t.Errorf("expected session ID sid-999 in role cache, got %s", sess.RoleCache["worker"])
	}
}

func TestEngineFailRoute(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-fail-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	engine := NewEngine(sm, newTestClient("echo", storageDir), "gemini", 2)

	skill := &models.SkillGraph{
		Name:         "fail-skill",
		InitialState: "step1",
		States: map[string]models.StateDef{
			"step1": {
				Type:        "action_loop",
				Instruction: "fail me",
				VerifyCmd:   "false", // Always fails
				OnFailRoute: "fixer",
			},
			"fixer": {
				Type: "end",
			},
		},
	}

	sess := &models.Session{
		ID:        "sess-fail",
		CWD:       ".",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	engine.Run(skill, sess)

	if sess.ActiveNode != "fixer" {
		t.Errorf("expected to be at 'fixer' node, got %s", sess.ActiveNode)
	}
	if sess.Status != "completed" {
		t.Errorf("expected 'completed' status, got %s", sess.Status)
	}
}

func TestEngineResolveInstruction(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-res-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	engine := NewEngine(sm, nil, "gemini", 5)
	instr := engine.ResolveInstruction("do something", ".")
	if instr != "do something" {
		t.Errorf("expected 'do something', got %s", instr)
	}

	// Test file instruction from skills dir
	skillsDir := filepath.Join(storageDir, "skills")
	os.MkdirAll(skillsDir, 0755)
	content := "---\ntitle: test\n---\nActual Instruction"
	os.WriteFile(filepath.Join(skillsDir, "instr.md"), []byte(content), 0644)

	instr = engine.ResolveInstruction("@instr.md", ".")
	if instr != "Actual Instruction" {
		t.Errorf("expected 'Actual Instruction', got '%s'", instr)
	}
}

func TestThoughtParser(t *testing.T) {
	var thoughts, text string
	parser := &ThoughtParser{
		OnThought: func(s string) { thoughts += s },
		OnText:    func(s string) { text += s },
	}

	// Normal case
	parser.Parse("Hello <thought>thinking</thought> World")
	if text != "Hello  World" || thoughts != "thinking" {
		t.Errorf("expected 'Hello  World' and 'thinking', got %q and %q", text, thoughts)
	}

	// Reset
	thoughts, text = "", ""
	parser = &ThoughtParser{
		OnThought: func(s string) { thoughts += s },
		OnText:    func(s string) { text += s },
	}

	// Split across chunks
	parser.Parse("Part 1 <tho")
	parser.Parse("ught>Hidden</thou")
	parser.Parse("ght> Part 2")
	if text != "Part 1  Part 2" || thoughts != "Hidden" {
		t.Errorf("expected 'Part 1  Part 2' and 'Hidden', got %q and %q", text, thoughts)
	}

	// Multiple thoughts
	thoughts, text = "", ""
	parser = &ThoughtParser{
		OnThought: func(s string) { thoughts += s },
		OnText:    func(s string) { text += s },
	}
	parser.Parse("<thought>1</thought>A<thought>2</thought>B")
	if text != "AB" || thoughts != "12" {
		t.Errorf("expected 'AB' and '12', got %q and %q", text, thoughts)
	}
}

func TestBuildPromptResumeBranch(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-bp-resume-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	engine := NewEngine(sm, newTestClient("echo", storageDir), "gemini", 5)

	state := &models.StateDef{Instruction: "Do the thing"}
	sess := &models.Session{
		ID:              "bp-resume",
		CWD:             ".",
		RoleCache:       make(map[string]string),
		PendingFeedback: "Session resumed. Please continue from where you left off.",
	}

	result := engine.BuildPrompt(state, sess)
	expected := "Do the thing\n\n### SESSION CONTEXT:\nSession resumed. Please continue from where you left off."
	if result != expected {
		t.Errorf("BuildPrompt resume branch:\n  got:  %q\n  want: %q", result, expected)
	}
}

func TestBuildPromptEmptyFeedback(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-bp-empty-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	engine := NewEngine(sm, newTestClient("echo", storageDir), "gemini", 5)

	state := &models.StateDef{Instruction: "Do the thing"}
	sess := &models.Session{
		ID:              "bp-empty",
		CWD:             ".",
		RoleCache:       make(map[string]string),
		PendingFeedback: "",
	}

	result := engine.BuildPrompt(state, sess)
	expected := "Do the thing"
	if result != expected {
		t.Errorf("BuildPrompt empty feedback:\n  got:  %q\n  want: %q", result, expected)
	}
}

func TestBuildPromptNormalFeedback(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-bp-normal-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	engine := NewEngine(sm, newTestClient("echo", storageDir), "gemini", 5)

	state := &models.StateDef{Instruction: "Do the thing"}
	sess := &models.Session{
		ID:              "bp-normal",
		CWD:             ".",
		RoleCache:       make(map[string]string),
		PendingFeedback: "Error: file not found",
	}

	result := engine.BuildPrompt(state, sess)
	expected := "Do the thing\n\n### FEEDBACK FROM PREVIOUS ATTEMPT:\nError: file not found"
	if result != expected {
		t.Errorf("BuildPrompt normal feedback:\n  got:  %q\n  want: %q", result, expected)
	}
}

func TestEngineExecutePrompt(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-prompt-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)

	dummyScript := `#!/bin/bash
echo '{"type": "init", "session_id": "sid-default"}'
echo '{"type": "message", "content": "Direct response"}'
`
	scriptPath := filepath.Join(storageDir, "dummy.sh")
	os.WriteFile(scriptPath, []byte(dummyScript), 0755)

	engine := NewEngine(sm, newTestClient(scriptPath, storageDir), "gemini", 5)

	sess := &models.Session{
		ID:        "sess-prompt",
		CWD:       ".",
		RoleCache: make(map[string]string),
	}
	sm.Save(sess)

	engine.ExecutePrompt(sess, "hello")

	// Wait a bit for Audit append (it's synchronous but disk I/O might be delayed in OS cache)
	time.Sleep(50 * time.Millisecond)

	if sess.RoleCache["default"] != "sid-default" {
		t.Errorf("expected sid-default in role cache, got %s", sess.RoleCache["default"])
	}

	audits, _ := sm.GetLastAudit(sess, 10)
	found := false
	for _, a := range audits {
		if a.Type == events.AuditLLMChunk && a.Content == "Direct response" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find LLM response in audit logs")
	}
}
