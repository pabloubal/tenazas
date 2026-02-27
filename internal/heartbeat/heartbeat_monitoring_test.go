package heartbeat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tenazas/internal/client"
	"tenazas/internal/engine"
	"tenazas/internal/models"
	"tenazas/internal/session"
)

type mockNotifier struct {
	notifications []mockNotification
}

type mockNotification struct {
	ChatID int64
	Text   string
}

func (m *mockNotifier) SendNotification(chatID int64, text string) {
	m.notifications = append(m.notifications, mockNotification{ChatID: chatID, Text: text})
}

func (m *mockNotifier) AllowedChatIDs() []int64 {
	return []int64{123}
}

func TestHeartbeatTriggerNewMonitoring(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-hb-mon-test-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	c, _ := client.NewClient("gemini", "echo", filepath.Join(storageDir, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	notif := &mockNotifier{}

	// Prepare skill
	os.MkdirAll(storageDir+"/skills/hb-skill", 0755)
	skill := models.SkillGraph{Name: "hb-skill", InitialState: "end", States: map[string]models.StateDef{"end": {Type: "end"}}}
	skillData, _ := json.Marshal(skill)
	os.WriteFile(storageDir+"/skills/hb-skill/skill.json", skillData, 0644)

	runner := NewRunner(storageDir, sm, eng, notif)
	hb := models.Heartbeat{Name: "Monitor HB", Skills: []string{"hb-skill"}, Path: storageDir}

	runner.Trigger(hb)

	// In the new system, Trigger should NOT send notifications directly for normal runs.
	if len(notif.notifications) > 0 {
		t.Errorf("Trigger should NOT send notifications directly (discrete notification)")
	}
}
