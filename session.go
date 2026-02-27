package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
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

func (sm *SessionManager) Create(cwd, title string) (*Session, error) {
	sess := &Session{
		ID:           uuid.New().String(),
		CWD:          cwd,
		Title:        title,
		LastUpdated:  time.Now(),
		RoleCache:    make(map[string]string),
		ApprovalMode: ApprovalModePlan,
		Status:       StatusIdle,
	}
	if err := sm.Save(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (sm *SessionManager) Log(s *Session, eventType, content string) {
	sm.AppendAudit(s, AuditEntry{
		Type:    eventType,
		Source:  "engine",
		Content: content,
	})
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

func (sm *SessionManager) metaPath(cwd, id string, archived bool) string {
	ext := ".meta.json"
	if archived {
		ext = ".meta.json.archive"
	}
	return filepath.Join(sm.storage.WorkspaceDir(cwd), id+ext)
}

type SessionIndexEntry struct {
	ID          string    `json:"id"`
	CWD         string    `json:"cwd"`
	Title       string    `json:"title"`
	LastUpdated time.Time `json:"last_updated"`
	Ephemeral   bool      `json:"ephemeral,omitempty"`
}

// updateGlobalIndex maintains a fast-lookup JSON file for session listings
func (sm *SessionManager) updateGlobalIndex(s *Session) {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".global_index.json")

	// Use a lock to prevent concurrent writes to the global index
	lockPath := indexPath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		fmt.Printf("⚠️ WARNING: Could not open global index lock file: %v\n", err)
		return
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		fmt.Printf("⚠️ WARNING: Could not acquire global index lock: %v\n", err)
		return
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	var index []SessionIndexEntry
	data, err := os.ReadFile(indexPath)
	if err == nil {
		_ = json.Unmarshal(data, &index)
	}

	found := false
	newIndex := make([]SessionIndexEntry, 0, len(index))
	for _, entry := range index {
		if entry.ID == s.ID {
			found = true
			if !s.Archived {
				newIndex = append(newIndex, SessionIndexEntry{s.ID, s.CWD, s.Title, s.LastUpdated, s.Ephemeral})
			}
			continue
		}
		newIndex = append(newIndex, entry)
	}
	if !found && !s.Archived {
		newIndex = append(newIndex, SessionIndexEntry{s.ID, s.CWD, s.Title, s.LastUpdated, s.Ephemeral})
	}

	// Keep it sorted by LastUpdated descending
	sort.Slice(newIndex, func(i, j int) bool {
		return newIndex[i].LastUpdated.After(newIndex[j].LastUpdated)
	})

	newData, _ := json.MarshalIndent(newIndex, "", "  ")
	if err := os.WriteFile(indexPath, newData, 0644); err != nil {
		fmt.Printf("⚠️ WARNING: Could not write global index: %v\x0a", err)
	}
}

func (sm *SessionManager) Save(s *Session) error {
	s.LastUpdated = time.Now()
	relPath := sm.metaPath(s.CWD, s.ID, s.Archived)
	if err := sm.storage.WriteJSON(relPath, s); err != nil {
		return err
	}
	sm.updateIndex(s.ID, s.CWD)
	sm.updateGlobalIndex(s)
	return nil
}

func (sm *SessionManager) Load(id string) (*Session, error) {
	// 1. Fast path: Index lookup
	if cwd := sm.getCWDFromIndex(id); cwd != "" {
		for _, archived := range []bool{false, true} {
			relPath := sm.metaPath(cwd, id, archived)
			var s Session
			if err := sm.storage.ReadJSON(relPath, &s); err == nil {
				return &s, nil
			}
		}
	}

	// 2. Fallback: Systematic scan (for recovery)
	root := filepath.Join(sm.StoragePath, "sessions")
	wdirs, _ := os.ReadDir(root)
	for _, wd := range wdirs {
		if !wd.IsDir() || wd.Name() == ".index" {
			continue
		}

		for _, archived := range []bool{false, true} {
			ext := ".meta.json"
			if archived {
				ext += ".archive"
			}

			relPath := filepath.Join("sessions", wd.Name(), id+ext)
			var s Session
			if err := sm.storage.ReadJSON(relPath, &s); err == nil {
				sm.updateIndex(id, s.CWD)
				return &s, nil
			}
		}
	}
	return nil, fmt.Errorf("session %s not found", id)
}

func (sm *SessionManager) Archive(id string) error {
	sess, err := sm.Load(id)
	if err != nil {
		return err
	}
	if sess.Archived {
		return nil
	}

	oldPath := filepath.Join(sm.StoragePath, sm.metaPath(sess.CWD, sess.ID, false))
	sess.Archived = true
	if err := sm.Save(sess); err != nil {
		return err
	}

	if err := os.Remove(oldPath); err != nil {
		fmt.Printf("⚠️ WARNING: Could not remove old session file during archive: %v\x0a", err)
	}
	return nil
}

func (sm *SessionManager) Rename(id string, newTitle string) error {
	sess, err := sm.Load(id)
	if err != nil {
		return err
	}
	sess.Title = newTitle
	return sm.Save(sess)
}

func (sm *SessionManager) GetLatest() (*Session, error) {
	sessions, _, err := sm.List(0, 1)
	if err != nil || len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}
	return &sessions[0], nil
}

func (sm *SessionManager) GetLatestSessionByTitle(title string) *Session {
	// Systematic scan to find the latest session with this title.
	// We can use the global index for this.
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".global_index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil
	}

	var index []SessionIndexEntry
	if err := json.Unmarshal(data, &index); err != nil {
		return nil
	}

	for _, entry := range index {
		if entry.Title == title {
			sess, err := sm.Load(entry.ID)
			if err == nil {
				return sess
			}
		}
	}
	return nil
}

func (sm *SessionManager) List(page, pageSize int) ([]Session, int, error) {
	return sm.listInternal(page, pageSize, true)
}

func (sm *SessionManager) ListActive(page, pageSize int) ([]Session, int, error) {
	return sm.listInternal(page, pageSize, false)
}

func (sm *SessionManager) listInternal(page, pageSize int, includeEphemeral bool) ([]Session, int, error) {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".global_index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		// Fallback to slow scan if index is missing
		return sm.slowListInternal(page, pageSize, includeEphemeral)
	}

	var index []SessionIndexEntry
	if err := json.Unmarshal(data, &index); err != nil {
		return sm.slowListInternal(page, pageSize, includeEphemeral)
	}

	// Filter based on includeEphemeral
	var filtered []SessionIndexEntry
	for _, entry := range index {
		if includeEphemeral || !entry.Ephemeral {
			filtered = append(filtered, entry)
		}
	}

	total := len(filtered)
	start := page * pageSize
	if start >= total {
		return nil, total, nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	sessions := make([]Session, 0, end-start)
	for _, entry := range filtered[start:end] {
		sess, err := sm.Load(entry.ID)
		if err == nil {
			sessions = append(sessions, *sess)
		}
	}
	return sessions, total, nil
}

func (sm *SessionManager) slowList(page, pageSize int) ([]Session, int, error) {
	return sm.slowListInternal(page, pageSize, true)
}

func (sm *SessionManager) slowListInternal(page, pageSize int, includeEphemeral bool) ([]Session, int, error) {
	fmt.Printf("⚠️ WARNING: Global index missing or corrupted. Triggering slow systematic scan...\n")
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
			// Only scan active meta files (not archives)
			if strings.HasSuffix(f.Name(), ".meta.json") {
				info, _ := f.Info()
				entries = append(entries, metaEntry{filepath.Join(subdir, f.Name()), info.ModTime()})
			}
		}
	}

	// Sort by modification time (descending)
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })

	var sessions []Session
	totalFiltered := 0

	start := page * pageSize

	// PERFORMANCE: Only unmarshal as many as we need for the current page.
	// We also limit the total entries scanned to prevent O(N) disasters.
	const maxScan = 500
	scanCount := 0

	for _, e := range entries {
		if scanCount >= maxScan {
			break
		}

		// To be even faster, we could peek at the file content for "ephemeral": true
		// but unmarshaling small JSONs is usually okay if we limit the count.
		var s Session
		data, _ := os.ReadFile(e.path)
		if json.Unmarshal(data, &s) == nil {
			scanCount++
			if includeEphemeral || !s.Ephemeral {
				if totalFiltered >= start && len(sessions) < pageSize {
					sessions = append(sessions, s)
				}
				totalFiltered++
			}
		}
	}

	return sessions, totalFiltered, nil
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

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	size := stat.Size()
	if size == 0 {
		return nil, nil
	}

	// Read in chunks from the end
	var lines []string
	const chunkSize = 4096
	buffer := make([]byte, chunkSize)
	offset := size
	leftover := ""

	for len(lines) < n && offset > 0 {
		readSize := int64(chunkSize)
		if offset < readSize {
			readSize = offset
		}
		offset -= readSize

		_, err := f.Seek(offset, 0)
		if err != nil {
			return nil, err
		}

		nRead, err := f.Read(buffer[:readSize])
		if err != nil && err != io.EOF {
			return nil, err
		}

		chunk := string(buffer[:nRead]) + leftover
		chunkLines := strings.Split(chunk, "\n")

		// If we are not at the beginning of the file, the first line of the split
		// might be incomplete (part of a line from the previous chunk).
		if offset > 0 {
			leftover = chunkLines[0]
			chunkLines = chunkLines[1:]
		} else {
			leftover = ""
		}

		// Prepend lines found in this chunk
		for i := len(chunkLines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(chunkLines[i])
			if line != "" {
				lines = append(lines, line)
				if len(lines) >= n {
					break
				}
			}
		}
	}

	// Reverse and decode
	var result []AuditEntry
	for i := len(lines) - 1; i >= 0; i-- {
		var entry AuditEntry
		if err := json.Unmarshal([]byte(lines[i]), &entry); err == nil {
			result = append(result, entry)
		}
	}

	return result, nil
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
