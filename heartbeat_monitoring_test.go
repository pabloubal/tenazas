package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
)

func TestHeartbeatTriggerNewMonitoring(t *testing.T) {
	// Mock Telegram Server
	mockServer := &mockTgServer{}
	server := httptest.NewServer(mockServer)
	defer server.Close()

	// Override base URL
	oldBaseURL := telegramBaseURL
	telegramBaseURL = server.URL + "/bot"
	defer func() { telegramBaseURL = oldBaseURL }()

	storageDir, _ := os.MkdirTemp("", "tenazas-hb-mon-test-*")
	defer os.RemoveAll(storageDir)

	sm := NewSessionManager(storageDir)
	reg, _ := NewRegistry(storageDir)
	exec := NewExecutor("echo", storageDir)
	engine := NewEngine(sm, exec, 5)

	tg := &Telegram{
		Token:      "mock-token",
		Sm:         sm,
		Reg:        reg,
		AllowedIDs: []int64{123},
	}

	// Prepare skill
	os.MkdirAll(storageDir+"/skills/hb-skill", 0755)
	skill := SkillGraph{Name: "hb-skill", InitialState: "end", States: map[string]StateDef{"end": {Type: "end"}}}
	skillData, _ := json.Marshal(skill)
	os.WriteFile(storageDir+"/skills/hb-skill/skill.json", skillData, 0644)

	runner := NewHeartbeatRunner(storageDir, sm, engine, tg, reg)
	hb := Heartbeat{Name: "Monitor HB", Skills: []string{"hb-skill"}, Path: storageDir}

	mockServer.mu.Lock()
	mockServer.calls = nil
	mockServer.mu.Unlock()

	runner.Trigger(hb)

	mockServer.mu.Lock()
	defer mockServer.mu.Unlock()

	// In the new system, Trigger should NOT call sendMessage directly.
	for _, call := range mockServer.calls {
		if call.Method == "sendMessage" {
			t.Errorf("Trigger should NOT call sendMessage directly (discrete notification)")
		}
	}
}
