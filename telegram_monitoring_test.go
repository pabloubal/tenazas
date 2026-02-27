package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestNotifyTaskState(t *testing.T) {
	// Mock Telegram Server
	mockServer := &mockTgServer{}
	server := httptest.NewServer(mockServer)
	defer server.Close()

	// Override base URL
	oldBaseURL := telegramBaseURL
	telegramBaseURL = server.URL + "/bot"
	defer func() { telegramBaseURL = oldBaseURL }()

	storageDir, _ := os.MkdirTemp("", "tenazas-monitoring-test-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	reg, _ := NewRegistry(storageDir)

	tg := &Telegram{
		Token:      "mock-token",
		Sm:         sm,
		Reg:        reg,
		AllowedIDs: []int64{123},
	}

	sess, _ := sm.Create("/tmp", "Monitoring Test")
	sess.ID = "test-monitoring-123"
	sm.Save(sess)

	t.Run("Initial Notification (SendMessage)", func(t *testing.T) {
		mockServer.mu.Lock()
		mockServer.calls = nil
		mockServer.mu.Unlock()

		tg.NotifyTaskState(sess.ID, TaskStateStarted, nil)

		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()

		if len(mockServer.calls) != 1 {
			t.Errorf("Expected 1 call, got %d", len(mockServer.calls))
			return
		}

		call := mockServer.calls[0]
		if call.Method != "sendMessage" {
			t.Errorf("Expected sendMessage, got %s", call.Method)
		}

		// Reload session to check updated IDs
		updatedSess, _ := sm.Load(sess.ID)
		if updatedSess.MonitoringMessageID == 0 {
			t.Errorf("Expected MonitoringMessageID to be set")
		}
		if updatedSess.MonitoringChatID != 123 {
			t.Errorf("Expected MonitoringChatID to be 123, got %d", updatedSess.MonitoringChatID)
		}
	})

	t.Run("Update Notification (EditMessage)", func(t *testing.T) {
		// Prepare session with existing message ID
		sess, _ = sm.Load(sess.ID)
		sess.MonitoringMessageID = 12345
		sess.MonitoringChatID = 123
		sm.Save(sess)

		mockServer.mu.Lock()
		mockServer.calls = nil
		mockServer.mu.Unlock()

		tg.NotifyTaskState(sess.ID, TaskStateBlocked, map[string]string{"reason": "Max retries reached"})

		mockServer.mu.Lock()
		defer mockServer.mu.Unlock()

		if len(mockServer.calls) != 1 {
			t.Errorf("Expected 1 call, got %d", len(mockServer.calls))
			return
		}

		call := mockServer.calls[0]
		if call.Method != "editMessageText" {
			t.Errorf("Expected editMessageText, got %s", call.Method)
		}

		text := call.Payload["text"].(string)
		if !strings.Contains(text, "Max retries reached") {
			t.Errorf("Expected text to contain reason, got %q", text)
		}

		// Verify keyboard contains Respond button
		replyMarkup := call.Payload["reply_markup"].(map[string]interface{})
		keyboard := replyMarkup["inline_keyboard"].([]interface{})
		row := keyboard[0].([]interface{})
		btn := row[0].(map[string]interface{})
		if !strings.Contains(btn["callback_data"].(string), "task_respond") {
			t.Errorf("Expected task_respond button, got %v", btn)
		}
	})
}

func TestHandleTaskCallbacks(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-callback-test-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	reg, _ := NewRegistry(storageDir)
	engine := &mockEngine{}

	tg := &Telegram{
		Sm:     sm,
		Reg:    reg,
		Engine: engine,
	}

	sess, _ := sm.Create("/tmp", "Callback Test")
	sess.ID = "test-cb-123"
	sess.Status = StatusRunning
	sm.Save(sess)

	chatID := int64(12345)

	t.Run("Task Pause", func(t *testing.T) {
		tg.HandleCallback(chatID, "act:task_pause:test-cb-123")

		updatedSess, _ := sm.Load("test-cb-123")
		if updatedSess.Status != StatusIdle {
			t.Errorf("Expected status to be %s, got %s", StatusIdle, updatedSess.Status)
		}
	})

	t.Run("Task Respond", func(t *testing.T) {
		tg.HandleCallback(chatID, "act:task_respond:test-cb-123")

		instanceID := fmt.Sprintf("tg-%d", chatID)
		state, _ := reg.Get(instanceID)
		if state.SessionID != "test-cb-123" {
			t.Errorf("Expected focused session to be test-cb-123, got %s", state.SessionID)
		}
	})
}
