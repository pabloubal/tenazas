package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tenazas/internal/config"
	"tenazas/internal/models"
	"tenazas/internal/storage"
)

// Load reads and resolves a skill from disk.
func Load(st *storage.Storage, skillName string, activeSkills []string) (*models.SkillGraph, error) {
	found := false
	for _, s := range activeSkills {
		if s == skillName {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("skill %s is disabled", skillName)
	}

	path := st.ResolveSkillPath(skillName)
	if path == "" {
		return nil, os.ErrNotExist
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var skill models.SkillGraph
	if err := json.Unmarshal(data, &skill); err != nil {
		return nil, err
	}

	skill.BaseDir = filepath.Dir(path)

	for name, state := range skill.States {
		if strings.HasPrefix(state.Instruction, "@") {
			resolved, err := st.ResolveInstruction(state.Instruction, skill.BaseDir)
			if err != nil {
				return nil, err
			}
			state.Instruction = resolved
		}
		if strings.HasPrefix(state.PreActionCmd, "@") {
			resolved, err := st.ResolveAssetPath(state.PreActionCmd, skill.BaseDir)
			if err != nil {
				return nil, err
			}
			state.PreActionCmd = resolved
		}
		if strings.HasPrefix(state.VerifyCmd, "@") {
			resolved, err := st.ResolveAssetPath(state.VerifyCmd, skill.BaseDir)
			if err != nil {
				return nil, err
			}
			state.VerifyCmd = resolved
		}
		if strings.HasPrefix(state.PostActionCmd, "@") {
			resolved, err := st.ResolveAssetPath(state.PostActionCmd, skill.BaseDir)
			if err != nil {
				return nil, err
			}
			state.PostActionCmd = resolved
		}
		skill.States[name] = state
	}

	return &skill, nil
}

// List returns the names of all discoverable skills.
func List(storageDir string) ([]string, error) {
	var skills []string

	dirs := []string{
		filepath.Join(storageDir, "skills"),
	}

	cwd, _ := os.Getwd()
	if absStorage, err := filepath.Abs(storageDir); err == nil {
		if absDefault, err := filepath.Abs(config.GetDefaultStoragePath()); err == nil && absStorage == absDefault {
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
