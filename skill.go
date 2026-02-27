package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (sm *SessionManager) LoadSkill(skillName string) (*SkillGraph, error) {
	// Check if skill is enabled
	active, _ := sm.GetActiveSkills()
	found := false
	for _, s := range active {
		if s == skillName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("skill %s is disabled", skillName)
	}

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

	skill.BaseDir = filepath.Dir(path)

	// Resolve instructions and commands
	for name, state := range skill.States {
		if strings.HasPrefix(state.Instruction, "@") {
			resolved, err := sm.storage.ResolveInstruction(state.Instruction, skill.BaseDir)
			if err != nil {
				return nil, err
			}
			state.Instruction = resolved
		}
		if strings.HasPrefix(state.PreActionCmd, "@") {
			resolved, err := sm.storage.ResolvePath(state.PreActionCmd, skill.BaseDir)
			if err != nil {
				return nil, err
			}
			state.PreActionCmd = resolved
		}
		if strings.HasPrefix(state.VerifyCmd, "@") {
			resolved, err := sm.storage.ResolvePath(state.VerifyCmd, skill.BaseDir)
			if err != nil {
				return nil, err
			}
			state.VerifyCmd = resolved
		}
		if strings.HasPrefix(state.PostActionCmd, "@") {
			resolved, err := sm.storage.ResolvePath(state.PostActionCmd, skill.BaseDir)
			if err != nil {
				return nil, err
			}
			state.PostActionCmd = resolved
		}
		skill.States[name] = state
	}

	return &skill, nil
}

// ResolveInstruction is a global helper for asset resolution
func ResolveInstruction(path, skillBaseDir, sessionCWD string) (string, error) {
	s := &Storage{}
	return s.ResolveInstruction(path, skillBaseDir)
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

	// Only add local skills if storageDir is the default storage path
	cwd, _ := os.Getwd()
	if absStorage, err := filepath.Abs(storageDir); err == nil {
		if absDefault, err := filepath.Abs(getDefaultStoragePath()); err == nil && absStorage == absDefault {
			if absCwd, err := filepath.Abs(cwd); err == nil && absStorage != absCwd {
				dirs = append(dirs, filepath.Join(absCwd, "skills"))
			}
		}
	}

	seen := make(map[string]bool)
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				if _, err := os.Stat(filepath.Join(dir, name, "skill.json")); err == nil {
					if !seen[name] {
						skills = append(skills, name)
						seen[name] = true
					}
				}
			} else if strings.HasSuffix(name, ".json") {
				name = strings.TrimSuffix(name, ".json")
				if !seen[name] {
					skills = append(skills, name)
					seen[name] = true
				}
			}
		}
	}
	return skills, nil
}
