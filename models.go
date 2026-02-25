package main

import (
	"os"
	"path/filepath"
	"time"
)

// Internal status constants
const (
	StatusRunning      = "running"
	StatusIntervention = "intervention_required"
	StatusCompleted    = "completed"
	StatusFailed       = "failed"
	StatusIdle         = "idle"
)

// Skill Definition (SKILL.json)
type SkillGraph struct {
	Name         string              `json:"skill_name"`
	BaseDir      string              `json:"base_dir,omitempty"` // Local directory for assets
	InitialState string              `json:"initial_state"`
	MaxLoops     int                 `json:"max_loops"` // Skill-wide loop limit
	States       map[string]StateDef `json:"states"`
}

type StateDef struct {
	Type          string `json:"type"` // "action_loop", "tool", "bridge", "end"
	SessionRole   string `json:"session_role"`
	Instruction   string `json:"instruction"`
	PreActionCmd  string `json:"pre_action_cmd,omitempty"`
	VerifyCmd     string `json:"verify_cmd,omitempty"`
	PostActionCmd string `json:"post_action_cmd,omitempty"`
	MaxRetries    int    `json:"max_retries"`
	OnFailPrompt  string `json:"on_fail_prompt,omitempty"`
	OnFailRoute   string `json:"on_fail_route,omitempty"`
	Next          string `json:"next,omitempty"`
	ApprovalMode  string `json:"approval_mode,omitempty"` // "plan", "auto_edit", "yolo"
	Command       string `json:"command,omitempty"`       // For "tool" type
	IsTerminal    bool   `json:"is_terminal,omitempty"`   // For "tool" type
}

// Session Metadata (.meta.json)
type Session struct {
	ID              string            `json:"id"`
	CWD             string            `json:"cwd"`
	Title           string            `json:"title"`
	SkillName       string            `json:"skill_name,omitempty"`
	LastUpdated     time.Time         `json:"last_updated"`
	ActiveNode      string            `json:"active_node"`
	RoleCache       map[string]string `json:"role_cache"` // Maps "planner" -> "gemini-sid-1"
	RetryCount      int               `json:"retry_count"`
	LoopCount       int               `json:"loop_count"` // Global loop counter
	Status          string            `json:"status"`     // "running", "intervention_required", "completed", "failed", "idle"
	PendingFeedback string            `json:"pending_feedback,omitempty"` // Context for next prompt
	Yolo            bool              `json:"yolo"`
}

// EnsureLocalDir creates a .tenazas directory in the session's CWD
func (s *Session) EnsureLocalDir() (string, error) {
	localDir := filepath.Join(s.CWD, ".tenazas")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return "", err
	}
	return localDir, nil
}


// Audit Entry (.audit.jsonl)
type AuditEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`   // AuditInfo, AuditLLMPrompt, etc.
	Source    string    `json:"source"` // "engine", "user", "gemini"
	Content   string    `json:"content"`
	ExitCode  int       `json:"exit_code,omitempty"`
}

// AuditFormatter defines how to render audit logs for different UIs
type AuditFormatter interface {
	Format(entry AuditEntry) string
}

// Heartbeat Definition
type Heartbeat struct {
	Name     string `json:"name"`
	Interval string `json:"interval"` // Cron string like "@hourly" or "every 5m"
	Path     string `json:"path"`
	Skill    string `json:"skill"`
}
