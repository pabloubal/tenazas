package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func LoadSkill(storageDir string, skillName string) (*SkillGraph, error) {
	// First try ~/.tenazas/skills/
	path := filepath.Join(storageDir, "skills", skillName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		// Fallback to local ./skills directory if running directly
		cwd, _ := os.Getwd()
		path = filepath.Join(cwd, "skills", skillName+".json")
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	}

	var skill SkillGraph
	if err := json.Unmarshal(data, &skill); err != nil {
		return nil, err
	}
	return &skill, nil
}

func ListSkills(storageDir string) ([]string, error) {
	var skills []string
	
	dirs := []string{
		filepath.Join(storageDir, "skills"),
		"skills",
	}

	for _, dir := range dirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if filepath.Ext(f.Name()) == ".json" {
				skills = append(skills, f.Name()[:len(f.Name())-5])
			}
		}
	}
	return skills, nil
}
