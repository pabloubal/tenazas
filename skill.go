package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func (sm *SessionManager) LoadSkill(skillName string) (*SkillGraph, error) {
	path := sm.storage.ResolveSkillPath(skillName)
	if path == "" {
		return nil, os.ErrNotExist
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var skill SkillGraph
	if err := json.Unmarshal(data, &skill); err != nil {
		return nil, err
	}
	return &skill, nil
}

// Maintaining the global LoadSkill for compatibility, but it should ideally use SessionManager
func LoadSkill(storageDir string, skillName string) (*SkillGraph, error) {
	sm := NewSessionManager(storageDir)
	return sm.LoadSkill(skillName)
}

func ListSkills(storageDir string) ([]string, error) {
	var skills []string
	
	dirs := []string{
		filepath.Join(storageDir, "skills"),
	}

	// Only add local skills if storageDir is not the current working directory
	cwd, _ := os.Getwd()
	if absStorage, err := filepath.Abs(storageDir); err == nil {
		if absCwd, err := filepath.Abs(cwd); err == nil && absStorage != absCwd {
			dirs = append(dirs, filepath.Join(absCwd, "skills"))
		}
	}

	seen := make(map[string]bool)
	for _, dir := range dirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) == ".json" {
				name := f.Name()[:len(f.Name())-5]
				if !seen[name] {
					skills = append(skills, name)
					seen[name] = true
				}
			}
		}
	}
	return skills, nil
}
