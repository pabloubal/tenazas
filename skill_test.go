package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillLoading(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-skill-test-*")
	defer os.RemoveAll(tmpDir)

	skillsDir := filepath.Join(tmpDir, "skills")
	os.MkdirAll(skillsDir, 0755)

	skill := SkillGraph{
		Name: "unique-test-skill",
		States: map[string]StateDef{
			"start": {Type: "end"},
		},
	}
	data, _ := json.Marshal(skill)
	os.WriteFile(filepath.Join(skillsDir, "unique-test-skill.json"), data, 0644)

	// Test LoadSkill
	loaded, err := LoadSkill(tmpDir, "unique-test-skill")
	if err != nil {
		t.Fatalf("failed to load skill: %v", err)
	}
	if loaded.Name != "unique-test-skill" {
		t.Errorf("expected skill name 'unique-test-skill', got %s", loaded.Name)
	}

	// Test ListSkills
	list, err := ListSkills(tmpDir)
	if err != nil {
		t.Fatalf("failed to list skills: %v", err)
	}
	found := false
	for _, s := range list {
		if s == "unique-test-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected to find 'unique-test-skill' in %v", list)
	}
}
