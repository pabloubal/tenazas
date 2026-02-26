package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type SessionManager struct {
	StoragePath string
	storage     *Storage
}

func NewSessionManager(storagePath string) *SessionManager {
	return &SessionManager{
		StoragePath: storagePath,
		storage:     NewStorage(storagePath),
	}
}

// updateIndex tracks session ID -> CWD for fast O(1) lookups
func (sm *SessionManager) updateIndex(id, cwd string) {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".index")
	os.MkdirAll(indexPath, 0755)
	os.WriteFile(filepath.Join(indexPath, id), []byte(cwd), 0644)
}

func (sm *SessionManager) getCWDFromIndex(id string) string {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".index", id)
	if data, err := os.ReadFile(indexPath); err == nil {
		return string(data)
	}
	return ""
}

func (sm *SessionManager) Save(s *Session) error {
	s.LastUpdated = time.Now()
	relPath := filepath.Join(sm.storage.WorkspaceDir(s.CWD), s.ID+".meta.json")
	if err := sm.storage.WriteJSON(relPath, s); err != nil {
		return err
	}
	sm.updateIndex(s.ID, s.CWD)
	return nil
}

func (sm *SessionManager) Load(id string) (*Session, error) {
	// Fast path: Registry/Index lookup
	if cwd := sm.getCWDFromIndex(id); cwd != "" {
		relPath := filepath.Join(sm.storage.WorkspaceDir(cwd), id+".meta.json")
		var s Session
		if err := sm.storage.ReadJSON(relPath, &s); err == nil {
			return &s, nil
		}
	}

	// Fallback: Systematic scan (for recovery)
	root := filepath.Join(sm.StoragePath, "sessions")
	wdirs, _ := os.ReadDir(root)
	for _, wd := range wdirs {
		if !wd.IsDir() || wd.Name() == ".index" {
			continue
		}
		fPath := filepath.Join(root, wd.Name(), id+".meta.json")
		if _, err := os.Stat(fPath); err == nil {
			var s Session
			if err := sm.storage.ReadJSON(filepath.Join("sessions", wd.Name(), id+".meta.json"), &s); err == nil {
				sm.updateIndex(id, s.CWD) 
				return &s, nil
			}
		}
	}
	return nil, fmt.Errorf("session %s not found", id)
}

func (sm *SessionManager) GetLatest() (*Session, error) {
	sessions, _, err := sm.List(0, 1)
	if err != nil || len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}
	return &sessions[0], nil
}

func (sm *SessionManager) List(page, pageSize int) ([]Session, int, error) {
	root := filepath.Join(sm.StoragePath, "sessions")
	wdirs, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, err
	}

	type metaEntry struct {
		path string
		mod  time.Time
	}
	var entries []metaEntry

	for _, wd := range wdirs {
		if !wd.IsDir() || wd.Name() == ".index" {
			continue
		}
		subdir := filepath.Join(root, wd.Name())
		files, _ := os.ReadDir(subdir)
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".meta.json") {
				info, _ := f.Info()
				entries = append(entries, metaEntry{filepath.Join(subdir, f.Name()), info.ModTime()})
			}
		}
	}

	// Sort by modification time (descending)
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })

	total := len(entries)
	start := page * pageSize
	if start >= total {
		return nil, total, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	// PERFORMANCE: Only unmarshal requested page
	sessions := make([]Session, 0, end-start)
	for _, e := range entries[start:end] {
		var s Session
		data, _ := os.ReadFile(e.path)
		if json.Unmarshal(data, &s) == nil {
			sessions = append(sessions, s)
		}
	}

	return sessions, total, nil
}

func (sm *SessionManager) AppendAudit(s *Session, entry AuditEntry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	
	relDir := sm.storage.WorkspaceDir(s.CWD)
	fPath := filepath.Join(sm.StoragePath, relDir, s.ID+".audit.jsonl")
	
	// Atomic lock for the specific audit file
	lockPath := fPath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	f, err := os.OpenFile(fPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	data, _ := json.Marshal(entry)
	data = append(data, '\n')
	_, err = f.Write(data)

	// DRY: Publish events directly from the source of truth
	GlobalBus.Publish(Event{Type: EventAudit, SessionID: s.ID, Payload: entry})
	return err
}

func (sm *SessionManager) GetLastAudit(s *Session, n int) ([]AuditEntry, error) {
	relDir := sm.storage.WorkspaceDir(s.CWD)
	fPath := filepath.Join(sm.StoragePath, relDir, s.ID+".audit.jsonl")
	f, err := os.Open(fPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var all []AuditEntry
	decoder := json.NewDecoder(f)
	for decoder.More() {
		var entry AuditEntry
		if err := decoder.Decode(&entry); err == nil {
			all = append(all, entry)
		}
	}

	if n > len(all) {
		n = len(all)
	}
	return all[len(all)-n:], nil
}

func (sm *SessionManager) RefreshSkillRegistry() error {
	skills, err := ListSkills(sm.StoragePath)
	if err != nil {
		return err
	}

	registry := make(map[string]bool)
	_ = sm.storage.ReadJSON("skills_registry.json", &registry)

	changed := false
	for _, s := range skills {
		if _, ok := registry[s]; !ok {
			registry[s] = true
			changed = true
		}
	}

	if changed {
		return sm.storage.WriteJSON("skills_registry.json", registry)
	}
	return nil
}

func (sm *SessionManager) GetActiveSkills() ([]string, error) {
	registry := make(map[string]bool)
	_ = sm.storage.ReadJSON("skills_registry.json", &registry)

	// Also ensure we have all skills from disk
	skills, _ := ListSkills(sm.StoragePath)
	var active []string
	for _, s := range skills {
		enabled, ok := registry[s]
		if !ok || enabled {
			active = append(active, s)
		}
	}
	return active, nil
}

func (sm *SessionManager) ToggleSkill(name string, enabled bool) error {
	registry := make(map[string]bool)
	_ = sm.storage.ReadJSON("skills_registry.json", &registry)
	registry[name] = enabled
	return sm.storage.WriteJSON("skills_registry.json", registry)
}
