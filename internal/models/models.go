package models

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

// Approval modes
const (
	ApprovalModePlan     = "PLAN"
	ApprovalModeAutoEdit = "AUTO_EDIT"
	ApprovalModeYolo     = "YOLO"
)

// SkillGraph defines a skill as a state machine.
type SkillGraph struct {
	Name         string              `json:"skill_name"`
	BaseDir      string              `json:"base_dir,omitempty"`
	InitialState string              `json:"initial_state"`
	MaxLoops     int                 `json:"max_loops"`
	MaxBudgetUSD float64             `json:"max_budget_usd,omitempty"`
	States       map[string]StateDef `json:"states"`
}

// StateDef defines a single state within a SkillGraph.
type StateDef struct {
	Type          string `json:"type"`
	SessionRole   string `json:"session_role"`
	Instruction   string `json:"instruction"`
	PreActionCmd  string `json:"pre_action_cmd,omitempty"`
	VerifyCmd     string `json:"verify_cmd,omitempty"`
	PostActionCmd string `json:"post_action_cmd,omitempty"`
	MaxRetries    int    `json:"max_retries"`
	OnFailPrompt  string `json:"on_fail_prompt,omitempty"`
	OnFailRoute   string `json:"on_fail_route,omitempty"`
	Next          string `json:"next,omitempty"`
	ApprovalMode  string `json:"approval_mode,omitempty"`
	ModelTier     string `json:"model_tier,omitempty"`
	Command       string `json:"command,omitempty"`
	IsTerminal    bool   `json:"is_terminal,omitempty"`
}

// Session represents a Tenazas session.
type Session struct {
	ID                  string            `json:"id"`
	Client              string            `json:"client,omitempty"`
	CWD                 string            `json:"cwd"`
	Title               string            `json:"title"`
	SkillName           string            `json:"skill_name,omitempty"`
	LastUpdated         time.Time         `json:"last_updated"`
	ActiveNode          string            `json:"active_node"`
	RoleCache           map[string]string `json:"role_cache"`
	RetryCount          int               `json:"retry_count"`
	LoopCount           int               `json:"loop_count"`
	Status              string            `json:"status"`
	PendingFeedback     string            `json:"pending_feedback,omitempty"`
	Yolo                bool              `json:"yolo"`
	Archived            bool              `json:"archived,omitempty"`
	ApprovalMode        string            `json:"approval_mode,omitempty"`
	ModelTier           string            `json:"model_tier,omitempty"`
	MaxBudgetUSD        float64           `json:"max_budget_usd,omitempty"`
	MonitoringChatID    int64             `json:"monitoring_chat_id,omitempty"`
	MonitoringMessageID int64             `json:"monitoring_message_id,omitempty"`
	TaskID              string            `json:"task_id,omitempty"`
	Ephemeral           bool              `json:"ephemeral,omitempty"`
}

// EnsureLocalDir creates a .tenazas directory in the session's CWD.
func (s *Session) EnsureLocalDir() (string, error) {
	localDir := filepath.Join(s.CWD, ".tenazas")
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return "", err
	}
	return localDir, nil
}

// Heartbeat defines a scheduled skill execution.
type Heartbeat struct {
	Name     string   `json:"name"`
	Interval string   `json:"interval"`
	Path     string   `json:"path"`
	Skills   []string `json:"skills"`
}

// EngineInterface defines the contract for the engine, used by CLI and Telegram.
type EngineInterface interface {
	ExecutePrompt(sess *Session, prompt string)
	ExecuteCommand(sess *Session, cmd string)
	Run(skill *SkillGraph, sess *Session)
	ResolveIntervention(id, action string)
	IsRunning(sessionID string) bool
	CancelSession(sessionID string)
}
