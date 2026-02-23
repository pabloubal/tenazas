package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type Registry struct {
	Instances map[string]string `json:"instances"` // "cli-PID" or "tg-ChatID" -> SessionID
	lockPath  string
	regPath   string
}

func NewRegistry(storageDir string) (*Registry, error) {
	r := &Registry{
		Instances: make(map[string]string),
		lockPath:  filepath.Join(storageDir, ".registry.lock"),
		regPath:   filepath.Join(storageDir, "registry.json"),
	}

	// Create lock file if not exists
	f, err := os.OpenFile(r.lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	f.Close()

	return r, nil
}

func (r *Registry) withLock(fn func() error) error {
	f, err := os.OpenFile(r.lockPath, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Exclusive flock
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	// Load
	data, err := os.ReadFile(r.regPath)
	if err == nil {
		if err := json.Unmarshal(data, &r.Instances); err != nil {
			// fallback if file is empty or corrupted
			r.Instances = make(map[string]string)
		}
	}

	if err := fn(); err != nil {
		return err
	}

	// Save
	data, err = json.MarshalIndent(r.Instances, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.regPath, data, 0644)
}

func (r *Registry) Set(instanceID, sessionID string) error {
	return r.withLock(func() error {
		r.Instances[instanceID] = sessionID
		return nil
	})
}

func (r *Registry) Get(instanceID string) (string, error) {
	var sessionID string
	err := r.withLock(func() error {
		var ok bool
		sessionID, ok = r.Instances[instanceID]
		if !ok {
			return fmt.Errorf("instance not found")
		}
		return nil
	})
	return sessionID, err
}
