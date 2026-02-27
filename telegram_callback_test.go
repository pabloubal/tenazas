package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Mock Engine for tests
type mockEngineForCallback struct {
	resolvedID     string
	resolvedAction string
	runSkillName   string
	runSessID      string
}

func (m *mockEngineForCallback) ExecutePrompt(sess *Session, prompt string) {}
func (m *mockEngineForCallback) ExecuteCommand(sess *Session, cmd string)   {}
func (m *mockEngineForCallback) Run(skill *SkillGraph, sess *Session) {
	if skill != nil {
		m.runSkillName = skill.Name
	}
	if sess != nil {
		m.runSessID = sess.ID
	}
}
func (m *mockEngineForCallback) ResolveIntervention(id, action string) {
	m.resolvedID = id
	m.resolvedAction = action
}
func (m *mockEngineForCallback) IsRunning(sessionID string) bool { return false }

func TestHandleCallback_Tokenization(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-test-*")
	defer os.RemoveAll(tmpDir)

	sm := NewSessionManager(tmpDir)
	reg, _ := NewRegistry(tmpDir)
	engine := &mockEngineForCallback{}

	// Mock Telegram Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true,"result":{"message_id":123}}`)
	}))
	defer server.Close()

	// Redirect Telegram API to mock server
	oldBaseURL := telegramBaseURL
	telegramBaseURL = server.URL + "/bot"
	defer func() { telegramBaseURL = oldBaseURL }()

	tg := &Telegram{
		Token:  "test-token",
		Sm:     sm,
		Reg:    reg,
		Engine: engine,
	}

	chatID := int64(12345)
	instanceID := fmt.Sprintf("tg-%d", chatID)

	// Create a test session
	sess, _ := sm.Create(tmpDir, "Test Session")

	// Create a test skill
	skillDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "test-skill.json"), []byte(`{"skill_name":"test-skill","initial_state":"start","states":{"start":{"type":"end"}}}`), 0644)

	tests := []struct {
		name     string
		data     string
		validate func(t *testing.T)
	}{
		{
			name: "New Archive Callback",
			data: "archive_session:" + sess.ID,
			validate: func(t *testing.T) {
				s, _ := sm.Load(sess.ID)
				if s == nil || !s.Archived {
					t.Errorf("expected session %s to be archived", sess.ID)
				}
			},
		},
		{
			name: "Show Sessions Page 1",
			data: "show_sessions:1",
			validate: func(t *testing.T) {
				// No crash
			},
		},
		{
			name: "View Session",
			data: "view_session:" + sess.ID,
			validate: func(t *testing.T) {
				state, _ := reg.Get(instanceID)
				if state.SessionID != sess.ID {
					t.Errorf("expected focused session to be %s, got %s", sess.ID, state.SessionID)
				}
			},
		},
		{
			name: "Legacy Res Callback",
			data: "res:" + sess.ID,
			validate: func(t *testing.T) {
				state, _ := reg.Get(instanceID)
				if state.SessionID != sess.ID {
					t.Errorf("expected focused session (legacy) to be %s, got %s", sess.ID, state.SessionID)
				}
			},
		},
		{
			name: "Intervention Callback",
			data: "intv:retry:" + sess.ID,
			validate: func(t *testing.T) {
				if engine.resolvedID != sess.ID || engine.resolvedAction != "retry" {
					t.Errorf("expected intervention resolve for %s:retry, got %s:%s", sess.ID, engine.resolvedID, engine.resolvedAction)
				}
			},
		},
		{
			name: "Skill Run Callback",
			data: "skill:run:test-skill",
			validate: func(t *testing.T) {
				if engine.runSkillName != "test-skill" {
					t.Errorf("expected skill test-skill to be run, got %s", engine.runSkillName)
				}
			},
		},
		{
			name: "Empty Data",
			data: "",
			validate: func(t *testing.T) {
				// No crash
			},
		},
		{
			name: "Unknown Command",
			data: "unknown_cmd:random_data",
			validate: func(t *testing.T) {
				// No crash
			},
		},
		{
			name: "Action Callback Legacy",
			data: "act:archive:" + sess.ID,
			validate: func(t *testing.T) {
				// Archive should work even via legacy act:
				s, _ := sm.Load(sess.ID)
				if s == nil || !s.Archived {
					t.Errorf("expected session %s to be archived via act:archive", sess.ID)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset engine mock for each subtest
			engine.resolvedID = ""
			engine.resolvedAction = ""
			engine.runSkillName = ""
			engine.runSessID = ""

			// Load/Create session if it was archived in previous tests
			if tt.name != "New Archive Callback" && tt.name != "Action Callback Legacy" {
				sess, _ = sm.Load(sess.ID)
				if sess.Archived {
					sess.Archived = false
					sm.Save(sess)
				}
			}

			tg.HandleCallback(chatID, tt.data)
			tt.validate(t)
		})
	}
}
