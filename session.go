package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SessionManager struct {
	StoragePath string
}

func NewSessionManager(storagePath string) *SessionManager {
	return &SessionManager{StoragePath: storagePath}
}

func (sm *SessionManager) getWorkspaceDir(cwd string) string {
	slug := strings.ReplaceAll(cwd, "/", "-")
	if strings.HasPrefix(slug, "-") {
		slug = slug[1:]
	}
	return filepath.Join(sm.StoragePath, "sessions", slug)
}

func (sm *SessionManager) Save(s *Session) error {
	s.LastUpdated = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	dir := sm.getWorkspaceDir(s.CWD)
	os.MkdirAll(dir, 0755)
	fPath := filepath.Join(dir, s.ID+".meta.json")
	return os.WriteFile(fPath, data, 0644)
}

func (sm *SessionManager) Load(id string) (*Session, error) {
	// Since we don't know the CWD, we must scan the workspace dirs
	dir := filepath.Join(sm.StoragePath, "sessions")
	wdirs, _ := os.ReadDir(dir)
	for _, wd := range wdirs {
		if !wd.IsDir() {
			continue
		}
		fPath := filepath.Join(dir, wd.Name(), id+".meta.json")
		if data, err := os.ReadFile(fPath); err == nil {
			var s Session
			if err := json.Unmarshal(data, &s); err != nil {
				return nil, err
			}
			if s.RoleCache == nil {
				s.RoleCache = make(map[string]string)
			}
			return &s, nil
		}
	}
	return nil, fmt.Errorf("session not found")
}

func (sm *SessionManager) List(page, pageSize int) ([]Session, int, error) {
	dir := filepath.Join(sm.StoragePath, "sessions")
	wdirs, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}

	var sessions []Session
	for _, wd := range wdirs {
		if !wd.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(dir, wd.Name()))
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".meta.json") {
				id := f.Name()[:len(f.Name())-len(".meta.json")]
				s, err := sm.Load(id)
				if err == nil {
					sessions = append(sessions, *s)
				}
			}
		}
	}

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

func (sm *SessionManager) AppendAudit(s *Session, entry AuditEntry) error {
	entry.Timestamp = time.Now()
	dir := sm.getWorkspaceDir(s.CWD)
	os.MkdirAll(dir, 0755)
	fPath := filepath.Join(dir, s.ID+".audit.jsonl")
	f, err := os.OpenFile(fPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	
	GlobalBus.Publish(Event{
		Type:      EventAudit,
		SessionID: s.ID,
		Payload:   entry,
	})
	
	return err
}

func (sm *SessionManager) GetLastAudit(s *Session, n int) ([]AuditEntry, error) {
	dir := sm.getWorkspaceDir(s.CWD)
	fPath := filepath.Join(dir, s.ID+".audit.jsonl")
	f, err := os.Open(fPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var all []AuditEntry
	decoder := json.NewDecoder(f)
	for {
		var entry AuditEntry
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		all = append(all, entry)
	}

	if n > len(all) {
		n = len(all)
	}
	return all[len(all)-n:], nil
}
