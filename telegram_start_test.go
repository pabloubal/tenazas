package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type mockTgCall struct {
	Method  string
	Payload map[string]interface{}
}

func TestHandleStartCommand(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-tg-start-test-*")
	defer os.RemoveAll(storageDir)

	var lastCall mockTgCall
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		lastCall.Method = parts[len(parts)-1]
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &lastCall.Payload)
		w.Write([]byte(`{"ok": true}`))
	}))
	defer ts.Close()

	// Override the base URL
	originalURL := telegramBaseURL
	telegramBaseURL = ts.URL + "/"
	defer func() { telegramBaseURL = originalURL }()

	sm := NewSessionManager(storageDir)
	reg, _ := NewRegistry(storageDir)

	tg := &Telegram{
		Sm:  sm,
		Reg: reg,
	}

	chatID := int64(12345)
	tg.handleStartCommand(chatID)

	if lastCall.Method != "sendMessage" {
		t.Errorf("Expected sendMessage call, got %s", lastCall.Method)
	}
	text, _ := lastCall.Payload["text"].(string)
	if !strings.Contains(text, "Welcome to Tenazas!") {
		t.Errorf("Welcome message missing expected text, got: %q", text)
	}

	// Check for the inline keyboard
	replyMarkup, ok := lastCall.Payload["reply_markup"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected reply_markup in payload")
	}
	keyboard, ok := replyMarkup["inline_keyboard"].([]interface{})
	if !ok || len(keyboard) < 3 {
		t.Errorf("Expected at least 3 buttons in inline keyboard, got %d", len(keyboard))
	}
}

func TestShowSkillsMenu(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-tg-skills-test-*")
	defer os.RemoveAll(storageDir)

	var lastCall mockTgCall
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		lastCall.Method = parts[len(parts)-1]
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &lastCall.Payload)
		w.Write([]byte(`{"ok": true}`))
	}))
	defer ts.Close()

	originalURL := telegramBaseURL
	telegramBaseURL = ts.URL + "/"
	defer func() { telegramBaseURL = originalURL }()

	// Create a dummy skill
	skillsDir := filepath.Join(storageDir, "skills")
	os.MkdirAll(skillsDir, 0755)
	os.WriteFile(filepath.Join(skillsDir, "test-skill.json"), []byte(`{"skill_name": "test-skill"}`), 0644)

	sm := NewSessionManager(storageDir)
	tg := &Telegram{
		Sm: sm,
	}

	chatID := int64(12345)
	tg.showSkillsMenu(chatID)

	if lastCall.Method != "sendMessage" {
		t.Errorf("Expected sendMessage call, got %s", lastCall.Method)
	}

	replyMarkup, _ := lastCall.Payload["reply_markup"].(map[string]interface{})
	keyboard, _ := replyMarkup["inline_keyboard"].([]interface{})

	found := false
	for _, rowRaw := range keyboard {
		row := rowRaw.([]interface{})
		for _, btnRaw := range row {
			btn := btnRaw.(map[string]interface{})
			if strings.Contains(btn["callback_data"].(string), "skill:run:test-skill") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("Did not find 'skill:run:test-skill' button in keyboard")
	}
}

func TestHandleCommand_Start(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-tg-cmd-test-*")
	defer os.RemoveAll(storageDir)

	var lastMethod string
	var lastPayload map[string]interface{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		lastMethod = parts[len(parts)-1]
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &lastPayload)
		w.Write([]byte(`{"ok": true}`))
	}))
	defer ts.Close()

	originalURL := telegramBaseURL
	telegramBaseURL = ts.URL + "/"
	defer func() { telegramBaseURL = originalURL }()

	sm := NewSessionManager(storageDir)
	reg, _ := NewRegistry(storageDir)

	tg := &Telegram{
		Sm:  sm,
		Reg: reg,
	}

	chatID := int64(12345)
	instanceID := fmt.Sprintf("tg-%d", chatID)

	// Testing if /start command is recognized and calls handleStartCommand (which should call sendMessage)
	tg.handleCommand(chatID, instanceID, "/start")

	if lastMethod != "sendMessage" {
		t.Errorf("Expected sendMessage call from /start handler, got %s", lastMethod)
	}
	text, _ := lastPayload["text"].(string)
	if strings.Contains(text, "Unknown command") {
		t.Errorf("Expected welcome message, but got 'Unknown command' error")
	}
	if !strings.Contains(text, "Welcome to Tenazas!") {
		t.Errorf("Expected welcome message with 'Welcome to Tenazas!', got %q", text)
	}
}

func TestHandleCallback_NewButtons(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-tg-callback-test-*")
	defer os.RemoveAll(storageDir)

	var lastMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		lastMethod = parts[len(parts)-1]
		w.Write([]byte(`{"ok": true}`))
	}))
	defer ts.Close()

	originalURL := telegramBaseURL
	telegramBaseURL = ts.URL + "/"
	defer func() { telegramBaseURL = originalURL }()

	sm := NewSessionManager(storageDir)
	reg, _ := NewRegistry(storageDir)

	tg := &Telegram{
		Sm:  sm,
		Reg: reg,
	}

	chatID := int64(12345)

	t.Run("start_new_session", func(t *testing.T) {
		lastMethod = ""
		tg.HandleCallback(chatID, "start_new_session")
		// showResumeMenu should be called, which calls sendMessage
		if lastMethod != "sendMessage" {
			t.Errorf("Expected sendMessage call from start_new_session callback, got %s", lastMethod)
		}
	})

	t.Run("show_skills", func(t *testing.T) {
		lastMethod = ""
		tg.HandleCallback(chatID, "show_skills")
		// showSkillsMenu should be called, which calls sendMessage
		if lastMethod != "sendMessage" {
			t.Errorf("Expected sendMessage call from show_skills callback, got %s", lastMethod)
		}
	})

	t.Run("skill_run", func(t *testing.T) {
		lastMethod = ""
		// Create a session for it to use
		sess, _ := sm.Create("/tmp", "Test Sess")
		sm.Save(sess)
		reg.Set(fmt.Sprintf("tg-%d", chatID), sess.ID)

		tg.HandleCallback(chatID, "skill:run:test-skill")
		// startSkill should be called, which calls sendMessage
		if lastMethod != "sendMessage" {
			t.Errorf("Expected sendMessage call from skill:run callback, got %s", lastMethod)
		}
	})
}

func TestStartSkill_AutoFocus(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-tg-autofocus-test-*")
	defer os.RemoveAll(storageDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok": true}`))
	}))
	defer ts.Close()

	originalURL := telegramBaseURL
	telegramBaseURL = ts.URL + "/"
	defer func() { telegramBaseURL = originalURL }()

	sm := NewSessionManager(storageDir)
	reg, _ := NewRegistry(storageDir)
	engine := &mockEngine{}

	tg := &Telegram{
		Sm:     sm,
		Reg:    reg,
		Engine: engine,
	}

	// Create a skill file
	skillsDir := filepath.Join(storageDir, "skills")
	os.MkdirAll(skillsDir, 0755)
	os.WriteFile(filepath.Join(skillsDir, "test-skill.json"), []byte(`{"skill_name": "test-skill"}`), 0644)

	// Create a session but don't focus it
	sess, _ := sm.Create("/tmp", "Latest Session")
	sm.Save(sess)

	chatID := int64(12345)
	instanceID := fmt.Sprintf("tg-%d", chatID)

	t.Run("AutoFocus latest when none focused", func(t *testing.T) {
		// Ensure registry is empty for this instance
		reg.Set(instanceID, "")

		tg.startSkill(chatID, instanceID, "test-skill")

		state, _ := reg.Get(instanceID)
		if state.SessionID != sess.ID {
			t.Errorf("Expected session %s to be auto-focused, got %s", sess.ID, state.SessionID)
		}
	})

	t.Run("FallBack to latest when focused session is missing", func(t *testing.T) {
		// Set a non-existent session ID
		reg.Set(instanceID, "non-existent")

		tg.startSkill(chatID, instanceID, "test-skill")

		state, _ := reg.Get(instanceID)
		if state.SessionID != sess.ID {
			t.Errorf("Expected session %s to be auto-focused as fallback, got %s", sess.ID, state.SessionID)
		}
	})
}
