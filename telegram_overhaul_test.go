package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTelegramUXOverhaul(t *testing.T) {
	// 1. Setup Mock Environment
	storageDir, _ := os.MkdirTemp("", "tenazas-tg-overhaul-test-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	reg, _ := NewRegistry(storageDir)
	engine := &mockEngine{}

	// Mock Telegram Server
	mockServer := &mockTgServer{}
	ts := httptest.NewServer(mockServer)
	defer ts.Close()

	// Override the base URL for tests
	oldBaseURL := telegramBaseURL
	telegramBaseURL = ts.URL + "/bot"
	defer func() { telegramBaseURL = oldBaseURL }()

	tg := &Telegram{
		Token:      "test-token",
		AllowedIDs: []int64{12345},
		Sm:         sm,
		Reg:        reg,
		Engine:     engine,
	}

	chatID := int64(12345)
	instanceID := fmt.Sprintf("tg-%d", chatID)

	// 2.1 Test: /start Navigation (Ref: Plan 2.1)
	t.Run("Start Navigation", func(t *testing.T) {
		mockServer.mu.Lock()
		mockServer.calls = nil
		mockServer.mu.Unlock()

		tg.HandleMessage(chatID, "/start")

		mockServer.mu.Lock()
		if len(mockServer.calls) == 0 {
			t.Fatal("Expected call to Telegram API")
		}

		payload := mockServer.calls[0].Payload
		text := payload["text"].(string)
		if !strings.Contains(text, "Welcome") {
			t.Errorf("Expected welcome message, got %q", text)
		}

		replyMarkup := payload["reply_markup"].(map[string]interface{})
		keyboard := replyMarkup["inline_keyboard"].([]interface{})

		foundSessions := false
		foundSkills := false
		foundHelp := false

		for _, row := range keyboard {
			for _, btn := range row.([]interface{}) {
				b := btn.(map[string]interface{})
				label := b["text"].(string)
				data := b["callback_data"].(string)

				if strings.Contains(label, "My Sessions") && data == "show_sessions:0" {
					foundSessions = true
				}
				if strings.Contains(label, "Run Skill") && data == "show_skills" {
					foundSkills = true
				}
				if label == "❓ Help" && data == "help" {
					foundHelp = true
				}
			}
		}

		if !foundSessions {
			t.Error("Missing '📂 My Sessions' button")
		}
		if !foundSkills {
			t.Error("Missing '🛠 Run Skill' button")
		}
		if !foundHelp {
			t.Error("Missing '❓ Help' button")
		}
		mockServer.mu.Unlock()
	})

	// 2.2 Test: /sessions & Selection Flow (Ref: Plan 1.2, 2.2)
	t.Run("Sessions Menu & Pagination", func(t *testing.T) {
		for i := 1; i <= 15; i++ {
			s, _ := sm.Create("/tmp", fmt.Sprintf("Project %d", i))
			s.LastUpdated = time.Now().Add(time.Duration(-i) * time.Minute)
			_ = sm.Save(s)
		}

		mockServer.mu.Lock()
		mockServer.calls = nil
		mockServer.mu.Unlock()

		tg.HandleMessage(chatID, "/sessions")

		mockServer.mu.Lock()
		if len(mockServer.calls) == 0 {
			t.Fatal("Expected call to /sessions")
		}

		payload := mockServer.calls[0].Payload
		replyMarkup := payload["reply_markup"].(map[string]interface{})
		keyboard := replyMarkup["inline_keyboard"].([]interface{})

		sessionBtnCount := 0
		hasNext := false
		for _, row := range keyboard {
			for _, btn := range row.([]interface{}) {
				b := btn.(map[string]interface{})
				data := b["callback_data"].(string)
				if strings.HasPrefix(data, "view_session:") {
					sessionBtnCount++
				}
				if data == "show_sessions:1" {
					hasNext = true
				}
			}
		}

		if sessionBtnCount < 8 || sessionBtnCount > 10 {
			t.Errorf("Expected 8-10 sessions, got %d", sessionBtnCount)
		}
		if !hasNext {
			t.Error("Missing 'Next' button")
		}
		mockServer.mu.Unlock()
	})

	t.Run("Session Selection", func(t *testing.T) {
		s, _ := sm.Create("/tmp", "Select Me")
		_ = sm.Save(s)

		tg.HandleCallback(chatID, "view_session:"+s.ID)

		state, _ := reg.Get(instanceID)
		if state.SessionID != s.ID {
			t.Errorf("Expected session %s to be focused", s.ID)
		}
	})

	// 2.3 Test: Context-Aware Keyboards (Ref: Plan 2.3)
	t.Run("Context-Aware Keyboards - With Bash", func(t *testing.T) {
		content := "Run this:\n```bash\nls -la\n```"

		mockServer.mu.Lock()
		mockServer.calls = nil
		mockServer.mu.Unlock()

		tg.broadcastAudit("sess-123", AuditEntry{
			Type:    AuditLLMResponse,
			Content: content,
		}, &HtmlFormatter{})

		mockServer.mu.Lock()
		lastCall := mockServer.calls[len(mockServer.calls)-1]
		keyboard := lastCall.Payload["reply_markup"].(map[string]interface{})["inline_keyboard"].([]interface{})

		foundRun := false
		for _, row := range keyboard {
			for _, btn := range row.([]interface{}) {
				if strings.Contains(btn.(map[string]interface{})["text"].(string), "▶️ Run: ls -la") {
					foundRun = true
				}
			}
		}
		if !foundRun {
			t.Error("Missing 'Run:' button")
		}
		mockServer.mu.Unlock()
	})

	t.Run("Context-Aware Keyboards - No Command", func(t *testing.T) {
		mockServer.mu.Lock()
		mockServer.calls = nil
		mockServer.mu.Unlock()

		tg.handleStreamingEnd(chatID, "sess-123", "Just text.", &HtmlFormatter{})

		mockServer.mu.Lock()
		lastCall := mockServer.calls[len(mockServer.calls)-1]
		keyboard := lastCall.Payload["reply_markup"].(map[string]interface{})["inline_keyboard"].([]interface{})

		for _, row := range keyboard {
			for _, btn := range row.([]interface{}) {
				if strings.Contains(btn.(map[string]interface{})["text"].(string), "Run:") {
					t.Error("Should NOT have 'Run:' button")
				}
			}
		}
		mockServer.mu.Unlock()
	})

	// 2.4 Test: Task Monitoring & Upsert (Ref: Plan 2.4)
	t.Run("Task Monitoring & Upsert", func(t *testing.T) {
		sess, _ := sm.Create("/tmp", "Monitor")
		_ = sm.Save(sess)

		mockServer.mu.Lock()
		mockServer.calls = nil
		mockServer.mu.Unlock()

		tg.NotifyTaskState(sess.ID, TaskStateStarted, nil)

		sess, _ = sm.Load(sess.ID)
		sess.MonitoringMessageID = 12345
		sess.MonitoringChatID = chatID
		_ = sm.Save(sess)

		tg.NotifyTaskState(sess.ID, TaskStateBlocked, map[string]string{"reason": "Wait"})

		mockServer.mu.Lock()
		foundEdit := false
		for _, call := range mockServer.calls {
			if call.Method == "editMessageText" && call.Payload["message_id"].(float64) == 12345 {
				foundEdit = true
			}
		}
		if !foundEdit {
			t.Error("Expected editMessageText")
		}
		mockServer.mu.Unlock()

		tg.HandleCallback(chatID, "act:task_respond:"+sess.ID)

		state, _ := reg.Get(instanceID)
		if state.SessionID != sess.ID {
			t.Error("Not focused")
		}
	})

	// 2.5 Test: Session Management Utilities (Ref: Plan 2.5)
	t.Run("More Actions Menu", func(t *testing.T) {
		sess, _ := sm.Create("/tmp", "Utility")
		_ = sm.Save(sess)

		mockServer.mu.Lock()
		mockServer.calls = nil
		mockServer.mu.Unlock()

		tg.HandleCallback(chatID, "act:more_actions:"+sess.ID)

		mockServer.mu.Lock()
		keyboard := mockServer.calls[0].Payload["reply_markup"].(map[string]interface{})["inline_keyboard"].([]interface{})

		foundYolo, foundArchive, foundRename := false, false, false
		for _, row := range keyboard {
			for _, btn := range row.([]interface{}) {
				b := btn.(map[string]interface{})
				txt := b["text"].(string)
				if strings.Contains(txt, "YOLO") {
					foundYolo = true
				}
				if strings.Contains(txt, "Archive") {
					foundArchive = true
				}
				if strings.Contains(txt, "Rename") {
					foundRename = true
				}
			}
		}
		if !foundYolo || !foundArchive || !foundRename {
			t.Error("Missing actions")
		}
		mockServer.mu.Unlock()
	})
}
