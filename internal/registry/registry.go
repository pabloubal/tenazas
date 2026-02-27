package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"tenazas/internal/storage"
)

type InstanceState struct {
	SessionID     string `json:"session_id"`
	Verbosity     string `json:"verbosity"`
	PendingAction string `json:"pending_action,omitempty"`
	PendingData   string `json:"pending_data,omitempty"`
}

type Registry struct {
	instances map[string]InstanceState
	storage   *storage.Storage
	lockPath  string
	mu        sync.RWMutex
}

func NewRegistry(storageDir string) (*Registry, error) {
	r := &Registry{
		instances: make(map[string]InstanceState),
		storage:   storage.NewStorage(storageDir),
		lockPath:  filepath.Join(storageDir, ".registry.lock"),
	}

	f, err := os.OpenFile(r.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	f.Close()

	r.Sync()
	return r, nil
}

func (r *Registry) Sync() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.storage.ReadJSON("registry.json", &r.instances)
}

func (r *Registry) withLock(fn func() error) error {
	f, err := os.OpenFile(r.lockPath, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.storage.ReadJSON("registry.json", &r.instances)
	return fn()
}

func (r *Registry) update(instanceID string, fn func(*InstanceState) bool) error {
	return r.withLock(func() error {
		state := r.instances[instanceID]
		if fn(&state) {
			r.instances[instanceID] = state
			return r.storage.WriteJSON("registry.json", r.instances)
		}
		return nil
	})
}

func (r *Registry) Set(instanceID, sessionID string) error {
	return r.update(instanceID, func(s *InstanceState) bool {
		if s.SessionID == sessionID {
			return false
		}
		s.SessionID = sessionID
		if s.Verbosity == "" {
			s.Verbosity = "MEDIUM"
		}
		return true
	})
}

func (r *Registry) SetVerbosity(instanceID, verbosity string) error {
	return r.update(instanceID, func(s *InstanceState) bool {
		if s.Verbosity == verbosity {
			return false
		}
		s.Verbosity = verbosity
		return true
	})
}

func (r *Registry) SetPending(instanceID, action, data string) error {
	return r.update(instanceID, func(s *InstanceState) bool {
		if s.PendingAction == action && s.PendingData == data {
			return false
		}
		s.PendingAction = action
		s.PendingData = data
		return true
	})
}

func (r *Registry) ClearPending(instanceID string) error {
	return r.update(instanceID, func(s *InstanceState) bool {
		if s.PendingAction == "" && s.PendingData == "" {
			return false
		}
		s.PendingAction = ""
		s.PendingData = ""
		return true
	})
}

func (r *Registry) Get(instanceID string) (InstanceState, error) {
	r.mu.RLock()
	state, ok := r.instances[instanceID]
	r.mu.RUnlock()

	if ok {
		return state, nil
	}

	if err := r.Sync(); err != nil {
		return InstanceState{}, err
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	if state, ok := r.instances[instanceID]; ok {
		return state, nil
	}
	return InstanceState{}, fmt.Errorf("instance %s not found", instanceID)
}
