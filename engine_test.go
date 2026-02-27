package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEngineBasic(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-test-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	exec := NewExecutor("echo", storageDir) // Dummy exec
	engine := NewEngine(sm, exec, 5)

	skill := &SkillGraph{
		Name:         "test-skill",
		InitialState: "start",
		States: map[string]StateDef{
			"start": {
				Type: "end",
			},
		},
	}

	sess := &Session{
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

	sm := NewSessionManager(storageDir)
	exec := NewExecutor("echo", storageDir)
	engine := NewEngine(sm, exec, 5)

	skill := &SkillGraph{
		Name:         "tool-skill",
		InitialState: "run-tool",
		States: map[string]StateDef{
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

	sess := &Session{
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

	sm := NewSessionManager(storageDir)
	exec := NewExecutor("echo", storageDir)
	engine := NewEngine(sm, exec, 5)

	skill := &SkillGraph{
		Name:         "interv-skill",
		InitialState: "wait",
		States: map[string]StateDef{
			"wait": {
				Type: "end",
			},
		},
	}

	sess := &Session{
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

	sm := NewSessionManager(storageDir)

	// Create a dummy binary that we can use for executor
	dummyScript := `#!/bin/bash
echo '{"type": "init", "session_id": "sid-999"}'
echo '{"type": "message", "content": "Done"}'
`
	scriptPath := filepath.Join(storageDir, "dummy.sh")
	os.WriteFile(scriptPath, []byte(dummyScript), 0755)

	exec := NewExecutor(scriptPath, storageDir)
	engine := NewEngine(sm, exec, 2)

	skill := &SkillGraph{
		Name:         "loop-skill",
		InitialState: "step1",
		States: map[string]StateDef{
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

	sess := &Session{
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

	sm := NewSessionManager(storageDir)
	exec := NewExecutor("echo", storageDir)
	engine := NewEngine(sm, exec, 2)

	skill := &SkillGraph{
		Name:         "fail-skill",
		InitialState: "step1",
		States: map[string]StateDef{
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

	sess := &Session{
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

	sm := NewSessionManager(storageDir)
	engine := NewEngine(sm, nil, 5)

	// Test literal instruction
	instr := engine.resolveInstruction("do something", ".")
	if instr != "do something" {
		t.Errorf("expected 'do something', got %s", instr)
	}

	// Test file instruction from skills dir
	skillsDir := filepath.Join(storageDir, "skills")
	os.MkdirAll(skillsDir, 0755)
	content := "---\ntitle: test\n---\nActual Instruction"
	os.WriteFile(filepath.Join(skillsDir, "instr.md"), []byte(content), 0644)

	instr = engine.resolveInstruction("@instr.md", ".")
	if instr != "Actual Instruction" {
		t.Errorf("expected 'Actual Instruction', got '%s'", instr)
	}
}

func TestThoughtParser(t *testing.T) {
	var thoughts, text string
	parser := &thoughtParser{
		onThought: func(s string) { thoughts += s },
		onText:    func(s string) { text += s },
	}

	// Normal case
	parser.parse("Hello <thought>thinking</thought> World")
	if text != "Hello  World" || thoughts != "thinking" {
		t.Errorf("expected 'Hello  World' and 'thinking', got %q and %q", text, thoughts)
	}

	// Reset
	thoughts, text = "", ""
	parser = &thoughtParser{
		onThought: func(s string) { thoughts += s },
		onText:    func(s string) { text += s },
	}

	// Split across chunks
	parser.parse("Part 1 <tho")
	parser.parse("ught>Hidden</thou")
	parser.parse("ght> Part 2")
	if text != "Part 1  Part 2" || thoughts != "Hidden" {
		t.Errorf("expected 'Part 1  Part 2' and 'Hidden', got %q and %q", text, thoughts)
	}

	// Multiple thoughts
	thoughts, text = "", ""
	parser = &thoughtParser{
		onThought: func(s string) { thoughts += s },
		onText:    func(s string) { text += s },
	}
	parser.parse("<thought>1</thought>A<thought>2</thought>B")
	if text != "AB" || thoughts != "12" {
		t.Errorf("expected 'AB' and '12', got %q and %q", text, thoughts)
	}
}

func TestEngineExecutePrompt(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-engine-prompt-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)

	dummyScript := `#!/bin/bash
echo '{"type": "init", "session_id": "sid-default"}'
echo '{"type": "message", "content": "Direct response"}'
`
	scriptPath := filepath.Join(storageDir, "dummy.sh")
	os.WriteFile(scriptPath, []byte(dummyScript), 0755)

	exec := NewExecutor(scriptPath, storageDir)
	engine := NewEngine(sm, exec, 5)

	sess := &Session{
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
		if a.Type == "llm_response_chunk" && a.Content == "Direct response" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find LLM response in audit logs")
	}
}
