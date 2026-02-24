package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type InstanceState struct {
	SessionID string `json:"session_id"`
	Verbosity string `json:"verbosity"` // "LOW", "MEDIUM", "HIGH"
}

type Registry struct {
	Instances map[string]InstanceState `json:"instances"`
	lockPath  string
	regPath   string
}

func NewRegistry(storageDir string) (*Registry, error) {
	r := &Registry{
		Instances: make(map[string]InstanceState),
		lockPath:  filepath.Join(storageDir, ".registry.lock"),
		regPath:   filepath.Join(storageDir, "registry.json"),
	}

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

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	data, err := os.ReadFile(r.regPath)
	if err == nil {
		if err := json.Unmarshal(data, &r.Instances); err != nil {
			r.Instances = make(map[string]InstanceState)
		}
	}

	if err := fn(); err != nil {
		return err
	}

	data, err = json.MarshalIndent(r.Instances, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.regPath, data, 0644)
}

func (r *Registry) Set(instanceID, sessionID string) error {
	return r.withLock(func() error {
		state := r.Instances[instanceID]
		state.SessionID = sessionID
		if state.Verbosity == "" {
			state.Verbosity = "MEDIUM" // Default
		}
		r.Instances[instanceID] = state
		return nil
	})
}

func (r *Registry) SetVerbosity(instanceID, verbosity string) error {
	return r.withLock(func() error {
		state := r.Instances[instanceID]
		state.Verbosity = verbosity
		r.Instances[instanceID] = state
		return nil
	})
}

func (r *Registry) Get(instanceID string) (InstanceState, error) {
	var state InstanceState
	err := r.withLock(func() error {
		var ok bool
		state, ok = r.Instances[instanceID]
		if !ok {
			return fmt.Errorf("instance not found")
		}
		return nil
	})
	return state, err
}
