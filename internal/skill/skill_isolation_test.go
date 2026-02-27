package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"tenazas/internal/session"
	"tenazas/internal/storage"
)

func TestSkillIsolation_Loading(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-skills-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	st := storage.NewStorage(tmpDir)
	sm := session.NewManager(tmpDir)

	// Setup isolated skill directory
	skillName := "my-skill"
	skillDir := filepath.Join(tmpDir, "skills", skillName)
	err = os.MkdirAll(skillDir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	skillJSON := `{
		"skill_name": "my-skill",
		"states": {
			"start": {
				"instruction": "@instructions.md",
				"verify_cmd": "@scripts/verify.sh"
			}
		}
	}`
	err = os.WriteFile(filepath.Join(skillDir, "skill.json"), []byte(skillJSON), 0644)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(skillDir, "instructions.md"), []byte("my instruction"), 0644)
	os.MkdirAll(filepath.Join(skillDir, "scripts"), 0755)
	os.WriteFile(filepath.Join(skillDir, "scripts", "verify.sh"), []byte("#!/bin/bash\necho ok"), 0755)

	// Test ResolveSkillPath for new structure
	path := st.ResolveSkillPath(skillName)
	expectedPath := filepath.Join(skillDir, "skill.json")
	if path != expectedPath {
		t.Errorf("Expected skill path %s, got %s", expectedPath, path)
	}

	// Test LoadSkill returns BaseDir
	_, err = sm.LoadSkill(skillName)
	if err != nil {
		t.Fatalf("Failed to load skill: %v", err)
	}
}

func TestResolveInstruction_PathSafety(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-safety-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	skillBaseDir := filepath.Join(tmpDir, "my-skill")
	os.MkdirAll(skillBaseDir, 0755)
	os.WriteFile(filepath.Join(skillBaseDir, "instructions.md"), []byte("my instruction"), 0644)

	secretFile := filepath.Join(tmpDir, "secret.txt")
	os.WriteFile(secretFile, []byte("sensitive"), 0644)

	st := storage.NewStorage(tmpDir)

	tests := []struct {
		name         string
		path         string
		skillBaseDir string
		expectError  bool
	}{
		{
			name:         "Relative to skill",
			path:         "@instructions.md",
			skillBaseDir: skillBaseDir,
			expectError:  false,
		},
		{
			name:         "Path traversal attempt",
			path:         "@../secret.txt",
			skillBaseDir: skillBaseDir,
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := st.ResolveInstruction(tt.path, tt.skillBaseDir)
			if (err != nil) != tt.expectError {
				t.Errorf("ResolveInstruction() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}

func TestSkillRegistry_ManagementIsolation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-registry-iso-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a few skills
	skills := []string{"alpha", "beta", "gamma"}
	for _, s := range skills {
		dir := filepath.Join(tmpDir, "skills", s)
		os.MkdirAll(dir, 0755)
		os.WriteFile(filepath.Join(dir, "skill.json"), []byte(`{}`), 0644)
	}

	sm := session.NewManager(tmpDir)

	// Refresh registry should discover skills
	err = sm.RefreshSkillRegistry()
	if err != nil {
		t.Fatalf("RefreshSkillRegistry failed: %v", err)
	}

	// All should be enabled by default (or as per implementation)
	activeSkills, err := sm.GetActiveSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(activeSkills) != 3 {
		t.Errorf("Expected 3 active skills, got %d", len(activeSkills))
	}

	// Toggle a skill
	err = sm.ToggleSkill("beta", false)
	if err != nil {
		t.Fatal(err)
	}

	activeSkills, err = sm.GetActiveSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(activeSkills) != 2 {
		t.Errorf("Expected 2 active skills after toggle, got %d", len(activeSkills))
	}
}
