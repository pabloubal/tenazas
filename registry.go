package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type InstanceState struct {
	SessionID string `json:"session_id"`
	Verbosity string `json:"verbosity"` // "LOW", "MEDIUM", "HIGH"
}

type Registry struct {
	instances map[string]InstanceState
	storage   *Storage
	lockPath  string
	mu        sync.RWMutex
}

func NewRegistry(storageDir string) (*Registry, error) {
	r := &Registry{
		instances: make(map[string]InstanceState),
		storage:   NewStorage(storageDir),
		lockPath:  filepath.Join(storageDir, ".registry.lock"),
	}

	// Ensure lock file exists
	f, err := os.OpenFile(r.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	f.Close()

	r.Sync()
	return r, nil
}

// Sync force-reloads the registry from disk
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

	// Sync from disk before modification
	r.storage.ReadJSON("registry.json", &r.instances)
	return fn()
}

func (r *Registry) Set(instanceID, sessionID string) error {
	return r.withLock(func() error {
		state := r.instances[instanceID]
		if state.SessionID == sessionID {
			return nil // No change
		}
		state.SessionID = sessionID
		if state.Verbosity == "" {
			state.Verbosity = "MEDIUM"
		}
		r.instances[instanceID] = state
		return r.storage.WriteJSON("registry.json", r.instances)
	})
}

func (r *Registry) SetVerbosity(instanceID, verbosity string) error {
	return r.withLock(func() error {
		state := r.instances[instanceID]
		if state.Verbosity == verbosity {
			return nil
		}
		state.Verbosity = verbosity
		r.instances[instanceID] = state
		return r.storage.WriteJSON("registry.json", r.instances)
	})
}

func (r *Registry) Get(instanceID string) (InstanceState, error) {
	// Optimistic memory read
	r.mu.RLock()
	state, ok := r.instances[instanceID]
	r.mu.RUnlock()

	if ok {
		return state, nil
	}

	// Fallback to sync and retry
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
