// Package main implements Tenazas, a high-performance, zero-dependency gateway
// for the gemini CLI. It bridges terminal and Telegram interfaces with
// stateful session handoff and directory-aware reasoning.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Session struct {
	ID          string    `json:"id"`
	GeminiSID   string    `json:"gemini_sid"`
	CWD         string    `json:"cwd"`
	Title       string    `json:"title"`
	LastUpdated time.Time `json:"last_updated"`
	Yolo        bool      `json:"yolo"`
}

func (s *Session) LocalDir() string {
	return filepath.Join(s.CWD, ".tenazas")
}

func (s *Session) EnsureLocalDir() (string, error) {
	dir := s.LocalDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

type SessionManager struct {
	StoragePath string
}

func NewSessionManager(storagePath string) *SessionManager {
	return &SessionManager{StoragePath: storagePath}
}

func (sm *SessionManager) Save(s *Session) error {
	s.LastUpdated = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	fPath := filepath.Join(sm.StoragePath, "sessions", s.ID+".json")
	return os.WriteFile(fPath, data, 0644)
}

func (sm *SessionManager) Load(id string) (*Session, error) {
	fPath := filepath.Join(sm.StoragePath, "sessions", id+".json")
	data, err := os.ReadFile(fPath)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (sm *SessionManager) List(page, pageSize int) ([]Session, int, error) {
	dir := filepath.Join(sm.StoragePath, "sessions")
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}

	var sessions []Session
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			s, err := sm.Load(f.Name()[:len(f.Name())-len(".json")])
			if err == nil {
				sessions = append(sessions, *s)
			}
		}
	}

	// Sort by newest
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastUpdated.After(sessions[j].LastUpdated)
	})

	total := len(sessions)
	start := page * pageSize
	if start >= total {
		return nil, total, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	return sessions[start:end], total, nil
}

func (sm *SessionManager) GetLatest() (*Session, error) {
	list, _, err := sm.List(0, 1)
	if err != nil || len(list) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}
	return &list[0], nil
}
