package heartbeat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tenazas/internal/client"
	"tenazas/internal/engine"
	"tenazas/internal/models"
	"tenazas/internal/session"
)

func TestHeartbeatCheckAndRun(t *testing.T) {
	storageDir, _ := os.MkdirTemp("", "tenazas-hb-test-*")
	defer os.RemoveAll(storageDir)

	sm := session.NewManager(storageDir)
	dummyScript := `#!/bin/bash
echo '{"type": "init", "session_id": "sid-hb"}'
echo '{"type": "message", "content": "hb done"}'
`
	scriptPath := filepath.Join(storageDir, "dummy.sh")
	os.WriteFile(scriptPath, []byte(dummyScript), 0755)

	c, _ := client.NewClient("gemini", scriptPath, filepath.Join(storageDir, "tenazas.log"))
	clients := map[string]client.Client{"gemini": c}
	eng := engine.NewEngine(sm, clients, "gemini", 5)

	hbDir := filepath.Join(storageDir, "heartbeats")
	os.MkdirAll(hbDir, 0755)

	skillsDir := filepath.Join(storageDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	skill := models.SkillGraph{
		Name:         "hb-skill",
		InitialState: "end",
		States: map[string]models.StateDef{
			"end": {Type: "end"},
		},
	}
	skillDir := filepath.Join(skillsDir, "hb-skill")
	os.MkdirAll(skillDir, 0755)
	skillData, _ := json.Marshal(skill)
	os.WriteFile(filepath.Join(skillDir, "skill.json"), skillData, 0644)

	hb := models.Heartbeat{
		Name:   "Test HB",
		Skills: []string{"hb-skill"},
		Path:   storageDir, // Use absolute path
	}
	hbData, _ := json.Marshal(hb)
	os.WriteFile(filepath.Join(hbDir, "test.json"), hbData, 0644)

	runner := NewRunner(storageDir, sm, eng, nil)
	runner.CheckAndRun()

	// Wait for background execution (increased for stability)
	time.Sleep(500 * time.Millisecond)

	list, _, _ := sm.List(0, 10)
	found := false
	for _, s := range list {
		t.Logf("Found session: %s - %s", s.ID, s.Title)
		if s.Title == "Heartbeat: Test HB" {
			found = true
			if s.Status != "completed" {
				t.Errorf("expected heartbeat session to be completed, got %s", s.Status)
			}
		}
	}
	if !found {
		t.Errorf("expected to find a session triggered by heartbeat in list of %d sessions", len(list))
	}
}
