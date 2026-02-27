package telegram

import (
	"fmt"
	"os"
	"testing"

	"tenazas/internal/events"
	"tenazas/internal/models"
	"tenazas/internal/registry"
	"tenazas/internal/session"
)

type mockEngine struct {
	executePromptCalled  bool
	lastPrompt           string
	executeCommandCalled bool
	lastCommand          string
}

func (m *mockEngine) ExecutePrompt(sess *models.Session, prompt string) {
	m.executePromptCalled = true
	m.lastPrompt = prompt
}

func (m *mockEngine) ExecuteCommand(sess *models.Session, cmd string) {
	m.executeCommandCalled = true
	m.lastCommand = cmd
}

func (m *mockEngine) Run(skill *models.SkillGraph, sess *models.Session) {}
func (m *mockEngine) ResolveIntervention(id, action string)             {}
func (m *mockEngine) IsRunning(sessionID string) bool                   { return false }

func TestHandleActionCallback(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-tg-act-test-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	reg, _ := registry.NewRegistry(storageDir)
	engine := &mockEngine{}

	tg := &Telegram{
		Sm:     sm,
		Reg:    reg,
		Engine: engine,
	}

	// Setup a session
	sess, _ := sm.Create("/tmp", "Test Session")
	sess.ID = "test-sess-123"
	sm.Save(sess)

	chatID := int64(12345)
	instanceID := fmt.Sprintf("tg-%d", chatID)
	reg.Set(instanceID, sess.ID)

	t.Run("Continue Prompt", func(t *testing.T) {
		engine.executePromptCalled = false
		tg.HandleCallback(chatID, "act:continue_prompt:test-sess-123")

		if !engine.executePromptCalled {
			t.Errorf("Expected ExecutePrompt to be called")
		}
		if engine.lastPrompt != "Continue" {
			t.Errorf("Expected prompt 'Continue', got %q", engine.lastPrompt)
		}
	})

	t.Run("New Session", func(t *testing.T) {
		tg.HandleCallback(chatID, "act:new_session:test-sess-123")

		state, _ := reg.Get(instanceID)
		if state.SessionID == "test-sess-123" {
			t.Errorf("Expected session ID to change in registry")
		}

		newSess, err := sm.Load(state.SessionID)
		if err != nil {
			t.Fatalf("Failed to load new session: %v", err)
		}
		if newSess.CWD != "/tmp" {
			t.Errorf("Expected new session to have same CWD /tmp, got %q", newSess.CWD)
		}
	})

	t.Run("Run Command", func(t *testing.T) {
		// Mock an audit log entry with a command
		sm.Log(sess, events.AuditLLMResponse, "Here is a command:\n```bash\nls -l\n```")

		engine.executeCommandCalled = false
		tg.HandleCallback(chatID, "act:run_command:test-sess-123")

		if !engine.executeCommandCalled {
			t.Errorf("Expected ExecuteCommand to be called")
		}
		if engine.lastCommand != "ls -l" {
			t.Errorf("Expected command 'ls -l', got %q", engine.lastCommand)
		}
	})
}

func TestHandleSessionManagement(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-tg-sess-test-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	reg, _ := registry.NewRegistry(storageDir)

	tg := &Telegram{
		Sm:  sm,
		Reg: reg,
	}

	chatID := int64(12345)
	instanceID := fmt.Sprintf("tg-%d", chatID)

	// Create some sessions
	s1, _ := sm.Create("/tmp/p1", "Session 1")
	s2, _ := sm.Create("/tmp/p2", "Session 2")
	sm.Save(s1)
	sm.Save(s2)

	t.Run("Archive Callback", func(t *testing.T) {
		tg.HandleCallback(chatID, fmt.Sprintf("act:archive:%s", s1.ID))

		loaded, _ := sm.Load(s1.ID)
		if !loaded.Archived {
			t.Errorf("Expected session %s to be archived", s1.ID)
		}

		_, total, _ := sm.List(0, 10)
		if total != 1 {
			t.Errorf("Expected 1 active session, got %d", total)
		}
	})

	t.Run("Rename Flow", func(t *testing.T) {
		// 1. Trigger rename action
		tg.HandleCallback(chatID, fmt.Sprintf("act:rename:%s", s2.ID))

		state, _ := reg.Get(instanceID)
		if state.PendingAction != "rename" || state.PendingData != s2.ID {
			t.Errorf("Expected pending rename action for session %s", s2.ID)
		}

		// 2. Simulate user sending the new name
		tg.HandleMessage(chatID, "Brand New Name")

		loaded, _ := sm.Load(s2.ID)
		if loaded.Title != "Brand New Name" {
			t.Errorf("Expected title 'Brand New Name', got %q", loaded.Title)
		}

		state, _ = reg.Get(instanceID)
		if state.PendingAction != "" {
			t.Errorf("Expected pending action to be cleared")
		}
	})

	t.Run("Resume Callback", func(t *testing.T) {
		tg.HandleCallback(chatID, fmt.Sprintf("act:resume:%s", s2.ID))

		state, _ := reg.Get(instanceID)
		if state.SessionID != s2.ID {
			t.Errorf("Expected session %s to be focused", s2.ID)
		}
	})
}
