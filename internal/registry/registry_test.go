package registry

import (
	"os"
	"testing"
)

func TestRegistrySetGet(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-registry-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	reg, err := NewRegistry(tmpDir)
	if err != nil {
		t.Fatalf("failed to create registry: %v", err)
	}

	instanceID := "test-instance"
	sessionID := "test-session-123"

	// Test Set
	err = reg.Set(instanceID, sessionID)
	if err != nil {
		t.Fatalf("failed to set instance state: %v", err)
	}

	// Test Get
	state, err := reg.Get(instanceID)
	if err != nil {
		t.Fatalf("failed to get instance state: %v", err)
	}

	if state.SessionID != sessionID {
		t.Errorf("expected session ID %s, got %s", sessionID, state.SessionID)
	}

	if state.Verbosity != "MEDIUM" {
		t.Errorf("expected default verbosity MEDIUM, got %s", state.Verbosity)
	}
}

func TestRegistryConcurrency(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-registry-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create two registry instances sharing the same directory (simulating different processes)
	reg1, _ := NewRegistry(tmpDir)
	reg2, _ := NewRegistry(tmpDir)

	err = reg1.Set("p1", "s1")
	if err != nil {
		t.Fatal(err)
	}

	err = reg2.Set("p2", "s2")
	if err != nil {
		t.Fatal(err)
	}

	s1, _ := reg2.Get("p1")
	if s1.SessionID != "s1" {
		t.Errorf("reg2 should see p1's state set by reg1")
	}

	s2, _ := reg1.Get("p2")
	if s2.SessionID != "s2" {
		t.Errorf("reg1 should see p2's state set by reg2")
	}
}

func TestRegistryVerbosity(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tenazas-registry-v-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	reg, _ := NewRegistry(tmpDir)
	err = reg.SetVerbosity("inst1", "HIGH")
	if err != nil {
		t.Fatal(err)
	}

	state, _ := reg.Get("inst1")
	if state.Verbosity != "HIGH" {
		t.Errorf("expected HIGH verbosity, got %s", state.Verbosity)
	}
}

func TestRegistryPendingActions(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-registry-p-*")
	defer os.RemoveAll(tmpDir)

	reg, _ := NewRegistry(tmpDir)
	instanceID := "test-instance"

	// Test SetPending
	err := reg.SetPending(instanceID, "rename", "sess-123")
	if err != nil {
		t.Fatalf("failed to set pending action: %v", err)
	}

	state, err := reg.Get(instanceID)
	if err != nil {
		t.Fatalf("failed to get state: %v", err)
	}
	if state.PendingAction != "rename" || state.PendingData != "sess-123" {
		t.Errorf("incorrect pending action: got %v, %v", state.PendingAction, state.PendingData)
	}

	// Test ClearPending
	err = reg.ClearPending(instanceID)
	if err != nil {
		t.Fatalf("failed to clear pending action: %v", err)
	}

	state, err = reg.Get(instanceID)
	if err != nil {
		t.Fatalf("failed to get state: %v", err)
	}
	if state.PendingAction != "" || state.PendingData != "" {
		t.Errorf("pending action not cleared: got %v, %v", state.PendingAction, state.PendingData)
	}
}
