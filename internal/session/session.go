package session

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

	"tenazas/internal/events"
	"tenazas/internal/models"
	"tenazas/internal/skill"
	"tenazas/internal/storage"
)

type Manager struct {
	StoragePath string
	Storage     *storage.Storage
}

func NewManager(storagePath string) *Manager {
	return &Manager{
		StoragePath: storagePath,
		Storage:     storage.NewStorage(storagePath),
	}
}

func (sm *Manager) Create(cwd, title string) (*models.Session, error) {
	sess := &models.Session{
		ID:           uuid.New().String(),
		CWD:          cwd,
		Title:        title,
		CreatedAt:    time.Now(),
		LastUpdated:  time.Now(),
		RoleCache:    make(map[string]string),
		ApprovalMode: models.ApprovalModePlan,
		Status:       models.StatusIdle,
	}
	if err := sm.Save(sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (sm *Manager) Log(s *models.Session, eventType, content string) {
	sm.AppendAudit(s, events.AuditEntry{
		Type:    eventType,
		Source:  "engine",
		Role:    events.RoleSystem,
		Content: content,
	})
}

func (sm *Manager) updateIndex(id, cwd string) {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".index")
	os.MkdirAll(indexPath, 0755)
	os.WriteFile(filepath.Join(indexPath, id), []byte(cwd), 0644)
}

func (sm *Manager) getCWDFromIndex(id string) string {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".index", id)
	if data, err := os.ReadFile(indexPath); err == nil {
		return string(data)
	}
	return ""
}

func (sm *Manager) metaPath(cwd, id string, archived bool) string {
	ext := ".meta.json"
	if archived {
		ext = ".meta.json.archive"
	}
	return filepath.Join(sm.Storage.WorkspaceDir(cwd), id+ext)
}

type IndexEntry struct {
	ID          string    `json:"id"`
	CWD         string    `json:"cwd"`
	Title       string    `json:"title"`
	LastUpdated time.Time `json:"last_updated"`
	Ephemeral   bool      `json:"ephemeral,omitempty"`
}

func (sm *Manager) updateGlobalIndex(s *models.Session) {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".global_index.json")

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

	var index []IndexEntry
	data, err := os.ReadFile(indexPath)
	if err == nil {
		_ = json.Unmarshal(data, &index)
	}

	found := false
	newIndex := make([]IndexEntry, 0, len(index))
	for _, entry := range index {
		if entry.ID == s.ID {
			found = true
			if !s.Archived {
				newIndex = append(newIndex, IndexEntry{s.ID, s.CWD, s.Title, s.LastUpdated, s.Ephemeral})
			}
			continue
		}
		newIndex = append(newIndex, entry)
	}
	if !found && !s.Archived {
		newIndex = append(newIndex, IndexEntry{s.ID, s.CWD, s.Title, s.LastUpdated, s.Ephemeral})
	}

	sort.Slice(newIndex, func(i, j int) bool {
		return newIndex[i].LastUpdated.After(newIndex[j].LastUpdated)
	})

	newData, _ := json.MarshalIndent(newIndex, "", "  ")
	if err := os.WriteFile(indexPath, newData, 0644); err != nil {
		fmt.Printf("⚠️ WARNING: Could not write global index: %v\n", err)
	}
}

func (sm *Manager) Save(s *models.Session) error {
	s.LastUpdated = time.Now()
	relPath := sm.metaPath(s.CWD, s.ID, s.Archived)
	if err := sm.Storage.WriteJSON(relPath, s); err != nil {
		return err
	}
	sm.updateIndex(s.ID, s.CWD)
	sm.updateGlobalIndex(s)
	return nil
}

func (sm *Manager) Load(id string) (*models.Session, error) {
	if cwd := sm.getCWDFromIndex(id); cwd != "" {
		for _, archived := range []bool{false, true} {
			relPath := sm.metaPath(cwd, id, archived)
			var s models.Session
			if err := sm.Storage.ReadJSON(relPath, &s); err == nil {
				return &s, nil
			}
		}
	}

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
			var s models.Session
			if err := sm.Storage.ReadJSON(relPath, &s); err == nil {
				sm.updateIndex(id, s.CWD)
				return &s, nil
			}
		}
	}
	return nil, fmt.Errorf("session %s not found", id)
}

func (sm *Manager) Archive(id string) error {
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
		fmt.Printf("⚠️ WARNING: Could not remove old session file during archive: %v\n", err)
	}
	return nil
}

func (sm *Manager) Rename(id string, newTitle string) error {
	sess, err := sm.Load(id)
	if err != nil {
		return err
	}
	sess.Title = newTitle
	return sm.Save(sess)
}

func (sm *Manager) GetLatest() (*models.Session, error) {
	sessions, _, err := sm.List(0, 1)
	if err != nil || len(sessions) == 0 {
		return nil, fmt.Errorf("no sessions found")
	}
	return &sessions[0], nil
}

func (sm *Manager) GetLatestSessionByTitle(title string) *models.Session {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".global_index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil
	}

	var index []IndexEntry
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

func (sm *Manager) List(page, pageSize int) ([]models.Session, int, error) {
	return sm.listInternal(page, pageSize, true)
}

func (sm *Manager) ListActive(page, pageSize int) ([]models.Session, int, error) {
	return sm.listInternal(page, pageSize, false)
}

func (sm *Manager) listInternal(page, pageSize int, includeEphemeral bool) ([]models.Session, int, error) {
	indexPath := filepath.Join(sm.StoragePath, "sessions", ".global_index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return sm.slowListInternal(page, pageSize, includeEphemeral)
	}

	var index []IndexEntry
	if err := json.Unmarshal(data, &index); err != nil {
		return sm.slowListInternal(page, pageSize, includeEphemeral)
	}

	var filtered []IndexEntry
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

	sessions := make([]models.Session, 0, end-start)
	for _, entry := range filtered[start:end] {
		sess, err := sm.Load(entry.ID)
		if err == nil {
			sessions = append(sessions, *sess)
		}
	}
	return sessions, total, nil
}

func (sm *Manager) slowListInternal(page, pageSize int, includeEphemeral bool) ([]models.Session, int, error) {
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
			if strings.HasSuffix(f.Name(), ".meta.json") {
				info, _ := f.Info()
				entries = append(entries, metaEntry{filepath.Join(subdir, f.Name()), info.ModTime()})
			}
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })

	var sessions []models.Session
	totalFiltered := 0

	start := page * pageSize

	const maxScan = 500
	scanCount := 0

	for _, e := range entries {
		if scanCount >= maxScan {
			break
		}

		var s models.Session
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

func (sm *Manager) AppendAudit(s *models.Session, entry events.AuditEntry) error {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	relDir := sm.Storage.WorkspaceDir(s.CWD)
	fPath := filepath.Join(sm.StoragePath, relDir, s.ID+".audit.jsonl")

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

	events.GlobalBus.Publish(events.Event{Type: events.EventAudit, SessionID: s.ID, Payload: entry})
	return err
}

// AuditPath returns the filesystem path to the session's audit JSONL file.
func (sm *Manager) AuditPath(s *models.Session) string {
	relDir := sm.Storage.WorkspaceDir(s.CWD)
	return filepath.Join(sm.StoragePath, relDir, s.ID+".audit.jsonl")
}

func (sm *Manager) GetLastAudit(s *models.Session, n int) ([]events.AuditEntry, error) {
	relDir := sm.Storage.WorkspaceDir(s.CWD)
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

		if offset > 0 {
			leftover = chunkLines[0]
			chunkLines = chunkLines[1:]
		} else {
			leftover = ""
		}

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

	var result []events.AuditEntry
	for i := len(lines) - 1; i >= 0; i-- {
		var entry events.AuditEntry
		if err := json.Unmarshal([]byte(lines[i]), &entry); err == nil {
			result = append(result, entry)
		}
	}

	return result, nil
}

func (sm *Manager) RefreshSkillRegistry() error {
	skills, err := skill.List(sm.StoragePath)
	if err != nil {
		return err
	}

	registry := make(map[string]bool)
	_ = sm.Storage.ReadJSON("skills_registry.json", &registry)

	changed := false
	for _, s := range skills {
		if _, ok := registry[s]; !ok {
			registry[s] = true
			changed = true
		}
	}

	if changed {
		return sm.Storage.WriteJSON("skills_registry.json", registry)
	}
	return nil
}

func (sm *Manager) GetActiveSkills() ([]string, error) {
	registry := make(map[string]bool)
	_ = sm.Storage.ReadJSON("skills_registry.json", &registry)

	skills, _ := skill.List(sm.StoragePath)
	var active []string
	for _, s := range skills {
		enabled, ok := registry[s]
		if !ok || enabled {
			active = append(active, s)
		}
	}
	return active, nil
}

func (sm *Manager) ToggleSkill(name string, enabled bool) error {
	registry := make(map[string]bool)
	_ = sm.Storage.ReadJSON("skills_registry.json", &registry)
	registry[name] = enabled
	return sm.Storage.WriteJSON("skills_registry.json", registry)
}

// LoadSkill is a convenience method that loads a skill checking the active registry.
func (sm *Manager) LoadSkill(skillName string) (*models.SkillGraph, error) {
	active, _ := sm.GetActiveSkills()
	return skill.Load(sm.Storage, skillName, active)
}
