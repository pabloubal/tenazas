package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tenazas/internal/client"
	"tenazas/internal/events"
	"tenazas/internal/models"
	"tenazas/internal/session"
)

const resumeSentinel = "Session resumed. Please continue from where you left off."

type Engine struct {
	Sm            *session.Manager
	Clients       map[string]client.Client
	DefaultClient string
	MaxLoops      int
	OnPermission  func(client.PermissionRequest) client.PermissionResponse // set by CLI/Telegram for interactive prompts
	intervs       map[string]chan string
	intervsMux    sync.RWMutex
	running       sync.Map
	cancelFns     sync.Map // sessionID -> context.CancelFunc
	sessionCtxs   sync.Map // sessionID -> context.Context
}

func NewEngine(sm *session.Manager, clients map[string]client.Client, defaultClient string, maxLoops int) *Engine {
	return &Engine{
		Sm:            sm,
		Clients:       clients,
		DefaultClient: defaultClient,
		MaxLoops:      maxLoops,
		intervs:       make(map[string]chan string),
		running:       sync.Map{},
	}
}

// resolveClient picks the right Client for a session.
func (e *Engine) resolveClient(sess *models.Session) client.Client {
	name := sess.Client
	if name == "" {
		name = e.DefaultClient
	}
	if c, ok := e.Clients[name]; ok {
		return c
	}
	if c, ok := e.Clients[e.DefaultClient]; ok {
		return c
	}
	for _, c := range e.Clients {
		return c
	}
	return nil
}

func (e *Engine) IsRunning(sessionID string) bool {
	_, ok := e.running.Load(sessionID)
	return ok
}

func (e *Engine) CancelSession(sessionID string) {
	if fn, ok := e.cancelFns.Load(sessionID); ok {
		fn.(context.CancelFunc)()
	}
}

func (e *Engine) Run(skill *models.SkillGraph, sess *models.Session) {
	if e.IsRunning(sess.ID) {
		return
	}
	e.running.Store(sess.ID, true)
	defer e.running.Delete(sess.ID)

	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFns.Store(sess.ID, cancel)
	e.sessionCtxs.Store(sess.ID, ctx)
	defer func() {
		cancel()
		e.cancelFns.Delete(sess.ID)
		e.sessionCtxs.Delete(sess.ID)
	}()

	e.publishTaskStatus(sess.ID, events.TaskStateStarted, nil)
	e.initializeExecution(skill, sess)

	for e.shouldContinue(sess) {
		state, ok := skill.States[sess.ActiveNode]
		if !ok {
			e.terminate(sess, models.StatusFailed, "State "+sess.ActiveNode+" not found")
			break
		}

		if state.Type == "end" {
			e.terminate(sess, models.StatusCompleted, "Skill completed successfully")
			break
		}

		if sess.Status == models.StatusIntervention {
			e.awaitIntervention(skill, &state, sess)
			if sess.Status != models.StatusRunning {
				continue
			}
		}

		switch state.Type {
		case "action_loop":
			e.executeActionLoop(skill, &state, sess)
		case "tool":
			e.executeTool(&state, sess)
		default:
			e.terminate(sess, models.StatusFailed, "Unknown state type: "+state.Type)
		}
	}
}

func (e *Engine) initializeExecution(skill *models.SkillGraph, sess *models.Session) {
	if sess.ActiveNode == "" {
		sess.ActiveNode = skill.InitialState
		sess.Status = models.StatusRunning
		sess.LoopCount = 0
		e.Sm.Save(sess)
		e.log(sess, events.AuditStatus, "engine", fmt.Sprintf("Started skill %s at node %s", skill.Name, sess.ActiveNode))
	} else if sess.Status == models.StatusRunning && sess.PendingFeedback == "" {
		sess.PendingFeedback = resumeSentinel
		e.Sm.Save(sess)
	}
}

func (e *Engine) shouldContinue(sess *models.Session) bool {
	if v, ok := e.sessionCtxs.Load(sess.ID); ok {
		if v.(context.Context).Err() != nil {
			return false
		}
	}
	return sess.Status == models.StatusRunning || sess.Status == models.StatusIntervention
}

func (e *Engine) terminate(sess *models.Session, status, reason string) {
	sess.Status = status
	e.log(sess, events.AuditStatus, "engine", fmt.Sprintf("Status: %s - %s", status, reason))
	e.Sm.Save(sess)

	state := events.TaskStateCompleted
	if status == models.StatusFailed {
		state = events.TaskStateFailed
	}
	e.publishTaskStatus(sess.ID, state, map[string]string{"reason": reason})
}

func (e *Engine) awaitIntervention(skill *models.SkillGraph, state *models.StateDef, sess *models.Session) {
	e.log(sess, events.AuditIntervention, "engine", fmt.Sprintf("Waiting for intervention at %s", sess.ActiveNode))

	e.publishTaskStatus(sess.ID, events.TaskStateBlocked, map[string]string{
		"node":        sess.ActiveNode,
		"instruction": state.Instruction,
		"reason":      sess.PendingFeedback,
	})

	action := <-e.getInterventionChan(sess.ID)

	switch action {
	case "retry":
		sess.RetryCount = 0
		sess.Status = models.StatusRunning
	case "proceed_to_fail":
		sess.RetryCount = 0
		sess.LoopCount = 0
		sess.Status = models.StatusRunning
		e.transitionToFailRoute(skill, state, sess, "User manually triggered fail route")
	case "abort":
		sess.Status = models.StatusFailed
	}
	e.Sm.Save(sess)
}

func (e *Engine) publishTaskStatus(sessID string, state string, details map[string]string) {
	events.GlobalBus.Publish(events.Event{
		Type:      events.EventTaskStatus,
		SessionID: sessID,
		Payload: events.TaskStatusPayload{
			State:   state,
			Details: details,
		},
	})
}

func (e *Engine) executeActionLoop(skill *models.SkillGraph, state *models.StateDef, sess *models.Session) {
	if state.PreActionCmd != "" && sess.RetryCount == 0 {
		if exitCode, output := e.RunShell(state.PreActionCmd, sess.CWD); exitCode != 0 {
			e.logCmd(sess, "engine", fmt.Sprintf("pre_action_cmd failed (Exit Code: %d): %s", exitCode, output), exitCode)
			e.handleRetry(state, sess, fmt.Sprintf("Pre-action command failed (Exit Code: %d):\n%s", exitCode, output))
			return
		}
	}

	response, err := e.callLLM(skill, state, sess)
	if err != nil {
		e.handleRetry(state, sess, "Client execution error: "+err.Error())
		return
	}
	sess.PendingFeedback = ""
	e.log(sess, events.AuditLLMResponse, state.SessionRole, response)

	if state.VerifyCmd == "" {
		e.completeState(state, sess, "")
		return
	}

	exitCode, output := e.RunShell(state.VerifyCmd, sess.CWD)
	e.logCmd(sess, "engine", fmt.Sprintf("Verification Result (Exit Code: %d):\n%s", exitCode, output), exitCode)

	if exitCode == 0 {
		e.completeState(state, sess, output)
	} else {
		e.handleLoopFailure(skill, state, sess, exitCode, output)
	}
}

func (e *Engine) executeTool(state *models.StateDef, sess *models.Session) {
	e.log(sess, events.AuditInfo, "engine", "Executing tool: "+state.Command)
	exitCode, out := e.RunShell(state.Command, sess.CWD)
	e.logCmd(sess, "engine", fmt.Sprintf("Exit Code: %d\nOutput: %s", exitCode, out), exitCode)

	sess.RetryCount = 0
	sess.PendingFeedback = out

	if exitCode == 0 {
		sess.ActiveNode = state.Next
	} else if state.OnFailRoute != "" {
		sess.ActiveNode = state.OnFailRoute
	} else {
		e.terminate(sess, models.StatusFailed, fmt.Sprintf("Tool failed (Exit Code: %d): %s", exitCode, out))
		return
	}
	e.Sm.Save(sess)
}

func (e *Engine) callLLM(skill *models.SkillGraph, state *models.StateDef, sess *models.Session) (string, error) {
	prompt := e.BuildPrompt(state, sess)
	e.log(sess, events.AuditLLMPrompt, state.SessionRole, prompt)

	roleID := sess.RoleCache[state.SessionRole]
	approvalMode := state.ApprovalMode
	if approvalMode == "" {
		approvalMode = sess.ApprovalMode
	}
	modelTier := state.ModelTier
	if modelTier == "" {
		modelTier = sess.ModelTier
	}

	// Skill-level budget overrides session-level.
	budget := sess.MaxBudgetUSD
	if skill != nil && skill.MaxBudgetUSD > 0 {
		budget = skill.MaxBudgetUSD
	}

	yolo := sess.Yolo || strings.EqualFold(approvalMode, models.ApprovalModeYolo)

	opts := client.RunOptions{
		NativeSID:    roleID,
		Prompt:       prompt,
		CWD:          sess.CWD,
		ApprovalMode: approvalMode,
		Yolo:         yolo,
		ModelTier:    modelTier,
		MaxBudgetUSD: budget,
		OnThought: func(t string) { e.log(sess, events.AuditLLMThought, state.SessionRole, t) },
		OnIntent:  func(text string) { e.log(sess, events.AuditIntent, state.SessionRole, text) },
		OnToolEvent: func(name, status, detail string) {
			msg := name
			if status != "" {
				msg += " [" + status + "]"
			}
			if detail != "" {
				msg += ": " + detail
			}
			e.log(sess, events.AuditCmdResult, state.SessionRole, msg)
		},
	}
	if !yolo && e.OnPermission != nil {
		opts.OnPermission = e.OnPermission
	}
	if v, ok := e.sessionCtxs.Load(sess.ID); ok {
		opts.Ctx = v.(context.Context)
	}

	onChunk := e.OnChunk(sess, state)
	c := e.resolveClient(sess)
	resp, err := c.Run(opts, onChunk, e.onSID(sess, state))
	onChunk("")
	return resp, err
}

func (e *Engine) handleRetry(state *models.StateDef, sess *models.Session, feedback string) {
	sess.RetryCount++
	if sess.PendingFeedback != "" && !strings.Contains(sess.PendingFeedback, feedback) {
		sess.PendingFeedback = sess.PendingFeedback + "\n\nAdditional Error: " + feedback
	} else {
		sess.PendingFeedback = feedback
	}

	if state.MaxRetries > 0 && sess.RetryCount >= state.MaxRetries {
		sess.Status = models.StatusIntervention
	}
	e.Sm.Save(sess)
}

func (e *Engine) handleLoopFailure(skill *models.SkillGraph, state *models.StateDef, sess *models.Session, exitCode int, output string) {
	sess.LoopCount++
	sess.RetryCount++

	feedback := state.OnFailPrompt
	if feedback == "" {
		feedback = "Command failed with exit code {{exit_code}}.\n\nOutput:\n{{output}}"
	}
	feedback = strings.ReplaceAll(feedback, "{{exit_code}}", fmt.Sprintf("%d", exitCode))
	feedback = strings.ReplaceAll(feedback, "{{output}}", output)
	feedback = strings.ReplaceAll(feedback, "{{stderr}}", output)
	feedback = strings.ReplaceAll(feedback, "{{stdout}}", output)

	limit := e.MaxLoops
	if skill.MaxLoops > 0 {
		limit = skill.MaxLoops
	}

	if sess.LoopCount >= limit {
		sess.PendingFeedback = feedback
		sess.Status = models.StatusIntervention
	} else if state.MaxRetries > 0 && sess.RetryCount <= state.MaxRetries {
		// Retry the same state with feedback before following fail_route
		sess.PendingFeedback = feedback
	} else {
		e.transitionToFailRoute(skill, state, sess, feedback)
	}
	e.Sm.Save(sess)
}

func (e *Engine) transitionToFailRoute(skill *models.SkillGraph, state *models.StateDef, sess *models.Session, feedback string) {
	if state.OnFailRoute == "" {
		sess.Status = models.StatusIntervention
		sess.PendingFeedback = feedback
		e.Sm.Save(sess)
		return
	}
	e.log(sess, events.AuditInfo, "engine", fmt.Sprintf("Fail route: %s (Loop %d)", state.OnFailRoute, sess.LoopCount))
	sess.PendingFeedback = feedback
	sess.ActiveNode = state.OnFailRoute
	sess.RetryCount = 0
}

func (e *Engine) completeState(state *models.StateDef, sess *models.Session, output string) {
	if state.PostActionCmd != "" {
		e.RunShell(state.PostActionCmd, sess.CWD)
	}
	sess.RetryCount = 0
	sess.LoopCount = 0
	sess.PendingFeedback = output
	sess.ActiveNode = state.Next
	e.Sm.Save(sess)
}

func (e *Engine) BuildPrompt(state *models.StateDef, sess *models.Session) string {
	instruction := e.ResolveInstruction(state.Instruction, sess.CWD)
	if sess.PendingFeedback == "" {
		return instruction
	}

	header := "### FEEDBACK FROM PREVIOUS ATTEMPT:"
	if sess.PendingFeedback == resumeSentinel {
		header = "### SESSION CONTEXT:"
	}
	return fmt.Sprintf("%s\n\n%s\n%s", instruction, header, sess.PendingFeedback)
}

func (e *Engine) OnChunk(sess *models.Session, state *models.StateDef) func(string) {
	parser := &ThoughtParser{
		OnThought: func(t string) { e.log(sess, events.AuditLLMThought, state.SessionRole, t) },
		OnText: func(t string) {
			e.Sm.AppendAudit(sess, events.AuditEntry{Type: events.AuditLLMChunk, Source: state.SessionRole, Content: t})
		},
	}
	return parser.Parse
}

func (e *Engine) onSID(sess *models.Session, state *models.StateDef) func(string) {
	return func(sid string) {
		sess.RoleCache[state.SessionRole] = sid
		e.Sm.Save(sess)
	}
}

func (e *Engine) ExecuteCommand(sess *models.Session, cmd string) {
	if e.IsRunning(sess.ID) {
		e.CancelSession(sess.ID)
		for i := 0; i < 50; i++ {
			if !e.IsRunning(sess.ID) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	e.running.Store(sess.ID, true)
	defer e.running.Delete(sess.ID)

	e.resumeAndRun(sess, func() {
		e.log(sess, events.AuditInfo, "user", fmt.Sprintf("User approved command: %s", cmd))
		exitCode, output := e.RunShell(cmd, sess.CWD)
		e.logCmd(sess, "engine", fmt.Sprintf("Exit Code: %d\n%s", exitCode, output), exitCode)
		e.executePromptInternal(sess, output)
	})
}

func (e *Engine) ExecutePrompt(sess *models.Session, prompt string) {
	// Cancel any in-flight prompt for this session before starting a new one
	if e.IsRunning(sess.ID) {
		e.CancelSession(sess.ID)
		// Wait briefly for the previous goroutine to release the running slot
		for i := 0; i < 50; i++ {
			if !e.IsRunning(sess.ID) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	e.running.Store(sess.ID, true)
	defer e.running.Delete(sess.ID)

	e.resumeAndRun(sess, func() {
		e.executePromptInternal(sess, prompt)
	})
}

func (e *Engine) resumeAndRun(sess *models.Session, f func()) {
	wasIntervention := sess.Status == models.StatusIntervention
	if sess.Status != models.StatusRunning {
		sess.Status = models.StatusRunning
		e.Sm.Save(sess)
	}

	f()

	if wasIntervention {
		e.ResolveIntervention(sess.ID, "retry")
	}
}

func (e *Engine) executePromptInternal(sess *models.Session, prompt string) {
	// Auto-set summary from first user prompt
	if sess.Summary == "" && prompt != "" {
		summary := prompt
		if len(summary) > 80 {
			summary = summary[:77] + "..."
		}
		sess.Summary = summary
		e.Sm.Save(sess)
	}

	e.log(sess, events.AuditLLMPrompt, "user", prompt)

	ctx, cancel := context.WithCancel(context.Background())
	e.cancelFns.Store(sess.ID, cancel)
	e.sessionCtxs.Store(sess.ID, ctx)
	defer func() {
		cancel()
		e.cancelFns.Delete(sess.ID)
		e.sessionCtxs.Delete(sess.ID)
	}()

	opts := client.RunOptions{
		Ctx:          ctx,
		NativeSID:    sess.RoleCache["default"],
		Prompt:       prompt,
		CWD:          sess.CWD,
		ApprovalMode: sess.ApprovalMode,
		Yolo:         sess.Yolo,
		ModelTier:    sess.ModelTier,
		MaxBudgetUSD: sess.MaxBudgetUSD,
		OnThought:    func(t string) { e.log(sess, events.AuditLLMThought, "default", t) },
		OnToolEvent: func(name, status, detail string) {
			msg := name
			if status != "" {
				msg += " [" + status + "]"
			}
			if detail != "" {
				msg += ": " + detail
			}
			e.log(sess, events.AuditCmdResult, "default", msg)
		},
	}
	if !sess.Yolo && e.OnPermission != nil {
		opts.OnPermission = e.OnPermission
	}

	onChunk := e.OnChunk(sess, &models.StateDef{SessionRole: "default"})
	c := e.resolveClient(sess)
	resp, err := c.Run(opts, onChunk, func(newSID string) {
		sess.RoleCache["default"] = newSID
		e.Sm.Save(sess)
	})
	onChunk("")

	if err != nil {
		if ctx.Err() == context.Canceled {
			e.log(sess, events.AuditInfo, "engine", "Operation cancelled by user")
		} else {
			e.log(sess, events.AuditInfo, "engine", "LLM Error: "+err.Error())
		}
	} else {
		e.log(sess, events.AuditLLMResponse, "default", resp)
	}
}

func (e *Engine) ResolveInstruction(instr, cwd string) string {
	if !strings.HasPrefix(instr, "@") {
		return instr
	}

	filename := strings.TrimPrefix(instr, "@")
	fullPath, err := e.Sm.Storage.ResolveAssetPath(instr, e.Sm.Storage.BaseDir)
	if err != nil {
		return "Error: " + err.Error()
	}

	paths := []string{
		filepath.Join(e.Sm.Storage.BaseDir, "skills", filename),
		filepath.Join(cwd, filename),
		fullPath,
	}

	for _, path := range paths {
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			if strings.HasPrefix(content, "---") {
				parts := strings.SplitN(content, "---", 3)
				if len(parts) == 3 {
					content = strings.TrimSpace(parts[2])
				}
			}
			return content
		}
	}

	return "Error: Could not load instruction file " + filename
}

func (e *Engine) RunShell(cmdStr, cwd string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()

	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else if ctx.Err() == context.DeadlineExceeded {
			exitCode = 124
			out = append(out, []byte("\nError: Command timed out after 30s")...)
		} else {
			exitCode = 1
		}
	}

	strOut := string(out)
	const maxLen = 32000
	if len(strOut) > maxLen {
		strOut = strOut[:1000] + "\n...[TRUNCATED]...\n" + strOut[len(strOut)-(maxLen-1100):]
	}
	return exitCode, strOut
}

func (e *Engine) log(sess *models.Session, eventType, source, content string) {
	e.Sm.AppendAudit(sess, events.AuditEntry{
		Type:    eventType,
		Source:  source,
		Content: content,
	})
}

func (e *Engine) logCmd(sess *models.Session, source, content string, exitCode int) {
	e.Sm.AppendAudit(sess, events.AuditEntry{
		Type:     events.AuditCmdResult,
		Source:   source,
		Content:  content,
		ExitCode: exitCode,
	})
}

func (e *Engine) getInterventionChan(sessID string) chan string {
	e.intervsMux.Lock()
	defer e.intervsMux.Unlock()
	ch, ok := e.intervs[sessID]
	if !ok {
		ch = make(chan string, 1)
		e.intervs[sessID] = ch
	}
	return ch
}

func (e *Engine) ResolveIntervention(sessID, action string) {
	e.intervsMux.RLock()
	ch, ok := e.intervs[sessID]
	e.intervsMux.RUnlock()

	if ok {
		select {
		case ch <- action:
		default:
		}
	}
}
