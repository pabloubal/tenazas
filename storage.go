package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Storage provides basic file operations for JSON models.
type Storage struct {
	BaseDir string
}

func NewStorage(baseDir string) *Storage {
	return &Storage{BaseDir: baseDir}
}

// WriteJSON saves any struct to a JSON file atomically (via temp file).
func (s *Storage) WriteJSON(relPath string, data interface{}) error {
	fullPath := filepath.Join(s.BaseDir, relPath)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tempFile := fullPath + ".tmp"
	f, err := os.OpenFile(tempFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		os.Remove(tempFile)
		return err
	}
	f.Close()

	return os.Rename(tempFile, fullPath)
}

// ReadJSON loads a struct from a JSON file.
func (s *Storage) ReadJSON(relPath string, out interface{}) error {
	fullPath := filepath.Join(s.BaseDir, relPath)
	f, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(out)
}

// Slugify maps a directory path to a safe filename slug.
func Slugify(path string) string {
	slug := strings.ReplaceAll(path, string(filepath.Separator), "-")
	if strings.HasPrefix(slug, "-") {
		slug = slug[1:]
	}
	return slug
}

// WorkspaceDir returns the path for a specific project/CWD.
func (s *Storage) WorkspaceDir(cwd string) string {
	return filepath.Join("sessions", Slugify(cwd))
}

func (s *Storage) resolvePath(dirs []string, filename string) string {
	for _, dir := range dirs {
		path := filepath.Join(dir, filename)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// ResolveSkillPath finds a skill file in storage or local project.
func (s *Storage) ResolveSkillPath(name string) string {
	return s.resolvePath([]string{filepath.Join(s.BaseDir, "skills"), "skills"}, name+".json")
}

// ResolveInstructionPath finds an instruction file in storage or session CWD.
func (s *Storage) ResolveInstructionPath(filename, sessionCWD string) string {
	return s.resolvePath([]string{filepath.Join(s.BaseDir, "skills"), sessionCWD}, filename)
}
