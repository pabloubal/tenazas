package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	sm         *SessionManager
	exec       *Executor
	maxLoops   int
	intervs    map[string]chan string
	intervsMux sync.RWMutex
	running    sync.Map // sessionID -> bool
}

func NewEngine(sm *SessionManager, exec *Executor, maxLoops int) *Engine {
	return &Engine{
		sm:       sm,
		exec:     exec,
		maxLoops: maxLoops,
		intervs:  make(map[string]chan string),
		running:  sync.Map{},
	}
}

func (e *Engine) IsRunning(sessionID string) bool {
	_, ok := e.running.Load(sessionID)
	return ok
}

func (e *Engine) Run(skill *SkillGraph, sess *Session) {
	if e.IsRunning(sess.ID) {
		return
	}
	e.running.Store(sess.ID, true)
	defer e.running.Delete(sess.ID)

	e.publishTaskStatus(sess.ID, TaskStateStarted, nil)
	e.initializeExecution(skill, sess)

	for e.shouldContinue(sess) {
		state, ok := skill.States[sess.ActiveNode]
		if !ok {
			e.terminate(sess, StatusFailed, "State "+sess.ActiveNode+" not found")
			break
		}

		if state.Type == "end" {
			e.terminate(sess, StatusCompleted, "Skill completed successfully")
			break
		}

		if sess.Status == StatusIntervention {
			e.awaitIntervention(skill, &state, sess)
			if sess.Status != StatusRunning {
				continue
			}
		}

		switch state.Type {
		case "action_loop":
			e.executeActionLoop(skill, &state, sess)
		case "tool":
			e.executeTool(&state, sess)
		default:
			e.terminate(sess, StatusFailed, "Unknown state type: "+state.Type)
		}
	}
}

func (e *Engine) initializeExecution(skill *SkillGraph, sess *Session) {
	if sess.ActiveNode == "" {
		sess.ActiveNode = skill.InitialState
		sess.Status = StatusRunning
		sess.LoopCount = 0
		e.sm.Save(sess)
		e.log(sess, AuditStatus, "engine", fmt.Sprintf("Started skill %s at node %s", skill.Name, sess.ActiveNode))
	} else if sess.Status == StatusRunning && sess.PendingFeedback == "" {
		sess.PendingFeedback = "Session resumed. Please continue from where you left off."
		e.sm.Save(sess)
	}
}

func (e *Engine) shouldContinue(sess *Session) bool {
	return sess.Status == StatusRunning || sess.Status == StatusIntervention
}

func (e *Engine) terminate(sess *Session, status, reason string) {
	sess.Status = status
	e.log(sess, AuditStatus, "engine", fmt.Sprintf("Status: %s - %s", status, reason))
	e.sm.Save(sess)

	state := TaskStateCompleted
	if status == StatusFailed {
		state = TaskStateFailed
	}
	e.publishTaskStatus(sess.ID, state, map[string]string{"reason": reason})
}

func (e *Engine) awaitIntervention(skill *SkillGraph, state *StateDef, sess *Session) {
	e.log(sess, AuditIntervention, "engine", fmt.Sprintf("Waiting for intervention at %s", sess.ActiveNode))

	e.publishTaskStatus(sess.ID, TaskStateBlocked, map[string]string{
		"node":        sess.ActiveNode,
		"instruction": state.Instruction,
		"reason":      sess.PendingFeedback,
	})

	action := <-e.getInterventionChan(sess.ID)

	switch action {
	case "retry":
		sess.RetryCount = 0
		sess.Status = StatusRunning
	case "proceed_to_fail":
		sess.RetryCount = 0
		sess.LoopCount = 0
		sess.Status = StatusRunning
		e.transitionToFailRoute(skill, state, sess, "User manually triggered fail route")
	case "abort":
		sess.Status = StatusFailed
	}
	e.sm.Save(sess)
}

func (e *Engine) publishTaskStatus(sessID string, state string, details map[string]string) {
	GlobalBus.Publish(Event{
		Type:      EventTaskStatus,
		SessionID: sessID,
		Payload: TaskStatusPayload{
			State:   state,
			Details: details,
		},
	})
}

func (e *Engine) executeActionLoop(skill *SkillGraph, state *StateDef, sess *Session) {
	// 1. Pre-Action
	if state.PreActionCmd != "" && sess.RetryCount == 0 {
		if exitCode, output := e.runShell(state.PreActionCmd, sess.CWD); exitCode != 0 {
			e.log(sess, AuditCmdResult, "engine", fmt.Sprintf("pre_action_cmd failed (Exit Code: %d): %s", exitCode, output))
			e.handleRetry(state, sess, fmt.Sprintf("Pre-action command failed (Exit Code: %d):\n%s", exitCode, output))
			return
		}
	}

	// 2. LLM Step
	response, err := e.callLLM(state, sess)
	if err != nil {
		e.handleRetry(state, sess, "Gemini execution error: "+err.Error())
		return
	}
	sess.PendingFeedback = "" // Clear feedback only after successful LLM consumption
	e.log(sess, AuditLLMResponse, state.SessionRole, response)

	// 3. Post-Action / Verification
	if state.VerifyCmd == "" {
		e.completeState(state, sess, "")
		return
	}

	exitCode, output := e.runShell(state.VerifyCmd, sess.CWD)
	e.log(sess, AuditCmdResult, "engine", fmt.Sprintf("Verification Result (Exit Code: %d):\x0a%s", exitCode, output))

	if exitCode == 0 {
		e.completeState(state, sess, output)
	} else {
		e.handleLoopFailure(skill, state, sess, exitCode, output)
	}
}

func (e *Engine) executeTool(state *StateDef, sess *Session) {
	e.log(sess, AuditInfo, "engine", "Executing tool: "+state.Command)
	exitCode, out := e.runShell(state.Command, sess.CWD)
	e.log(sess, AuditCmdResult, "engine", fmt.Sprintf("Exit Code: %d\x0aOutput: %s", exitCode, out))

	sess.RetryCount = 0
	sess.PendingFeedback = out

	if exitCode == 0 {
		sess.ActiveNode = state.Next
	} else if state.OnFailRoute != "" {
		sess.ActiveNode = state.OnFailRoute
	} else {
		e.terminate(sess, StatusFailed, fmt.Sprintf("Tool failed (Exit Code: %d): %s", exitCode, out))
		return
	}
	e.sm.Save(sess)
}

func (e *Engine) callLLM(state *StateDef, sess *Session) (string, error) {
	prompt := e.buildPrompt(state, sess)
	e.log(sess, AuditLLMPrompt, state.SessionRole, prompt)

	roleID := sess.RoleCache[state.SessionRole]
	approvalMode := state.ApprovalMode
	if approvalMode == "" {
		approvalMode = sess.ApprovalMode
	}
	onChunk := e.onChunk(sess, state)
	resp, err := e.exec.Run(roleID, prompt, sess.CWD, approvalMode, sess.Yolo, onChunk, e.onSID(sess, state))
	onChunk("")
	return resp, err
}

func (e *Engine) handleRetry(state *StateDef, sess *Session, feedback string) {
	sess.RetryCount++
	if sess.PendingFeedback != "" && !strings.Contains(sess.PendingFeedback, feedback) {
		sess.PendingFeedback = sess.PendingFeedback + "\n\nAdditional Error: " + feedback
	} else {
		sess.PendingFeedback = feedback
	}

	if state.MaxRetries > 0 && sess.RetryCount >= state.MaxRetries {
		sess.Status = StatusIntervention
	}
	e.sm.Save(sess)
}

func (e *Engine) handleLoopFailure(skill *SkillGraph, state *StateDef, sess *Session, exitCode int, output string) {
	sess.LoopCount++

	feedback := state.OnFailPrompt
	if feedback == "" {
		feedback = "Command failed with exit code {{exit_code}}.\x0a\x0aOutput:\x0a{{output}}"
	}
	feedback = strings.ReplaceAll(feedback, "{{exit_code}}", fmt.Sprintf("%d", exitCode))
	feedback = strings.ReplaceAll(feedback, "{{output}}", output)
	feedback = strings.ReplaceAll(feedback, "{{stderr}}", output)
	feedback = strings.ReplaceAll(feedback, "{{stdout}}", output)

	limit := e.maxLoops
	if skill.MaxLoops > 0 {
		limit = skill.MaxLoops
	}

	if sess.LoopCount >= limit {
		sess.PendingFeedback = feedback
		sess.Status = StatusIntervention
	} else {
		e.transitionToFailRoute(skill, state, sess, feedback)
	}
	e.sm.Save(sess)
}

func (e *Engine) transitionToFailRoute(skill *SkillGraph, state *StateDef, sess *Session, feedback string) {
	if state.OnFailRoute == "" {
		sess.Status = StatusIntervention
		sess.PendingFeedback = feedback
		e.sm.Save(sess)
		return
	}
	e.log(sess, AuditInfo, "engine", fmt.Sprintf("Fail route: %s (Loop %d)", state.OnFailRoute, sess.LoopCount))
	sess.PendingFeedback = feedback
	sess.ActiveNode = state.OnFailRoute
	sess.RetryCount = 0
}

func (e *Engine) completeState(state *StateDef, sess *Session, output string) {
	if state.PostActionCmd != "" {
		e.runShell(state.PostActionCmd, sess.CWD)
	}
	sess.RetryCount = 0
	sess.LoopCount = 0
	sess.PendingFeedback = output // Preserve output as feedback for next state
	sess.ActiveNode = state.Next
	e.sm.Save(sess)
}

func (e *Engine) buildPrompt(state *StateDef, sess *Session) string {
	instruction := e.resolveInstruction(state.Instruction, sess.CWD)
	if sess.PendingFeedback == "" {
		return instruction
	}

	// Handle special resume feedback
	if sess.PendingFeedback == "Session resumed. Please continue from where you left off." {
		return sess.PendingFeedback
	}

	return fmt.Sprintf("%s\x0a\x0a### FEEDBACK FROM PREVIOUS ATTEMPT:\x0a%s", instruction, sess.PendingFeedback)
}

func (e *Engine) onChunk(sess *Session, state *StateDef) func(string) {
	parser := &thoughtParser{
		onThought: func(t string) { e.log(sess, AuditLLMThought, state.SessionRole, t) },
		onText: func(t string) {
			e.sm.AppendAudit(sess, AuditEntry{Type: AuditLLMChunk, Source: state.SessionRole, Content: t})
		},
	}
	return parser.parse
}

type thoughtParser struct {
	inThought bool
	buffer    string
	onThought func(string)
	onText    func(string)
}

func (p *thoughtParser) parse(chunk string) {
	if chunk == "" {
		p.flush()
		return
	}

	p.buffer += chunk
	for {
		target := "<thought>"
		if p.inThought {
			target = "</thought>"
		}

		idx := strings.Index(p.buffer, target)
		if idx == -1 {
			possibleTagStart := strings.LastIndexAny(p.buffer, "<")
			if possibleTagStart != -1 {
				remaining := p.buffer[possibleTagStart:]
				if strings.HasPrefix(target, remaining) {
					p.emit(p.buffer[:possibleTagStart])
					p.buffer = remaining
					return
				}
			}
			p.emit(p.buffer)
			p.buffer = ""
			return
		}

		p.emit(p.buffer[:idx])
		p.buffer = p.buffer[idx+len(target):]
		p.inThought = !p.inThought
	}
}

func (p *thoughtParser) emit(text string) {
	if text == "" {
		return
	}
	if p.inThought {
		p.onThought(text)
	} else {
		p.onText(text)
	}
}

func (p *thoughtParser) flush() {
	if p.buffer == "" {
		return
	}
	p.emit(p.buffer)
	p.buffer = ""
}

func (e *Engine) onSID(sess *Session, state *StateDef) func(string) {
	return func(sid string) {
		sess.RoleCache[state.SessionRole] = sid
		e.sm.Save(sess)
	}
}

func (e *Engine) ExecuteCommand(sess *Session, cmd string) {
	e.resumeAndRun(sess, func() {
		e.log(sess, AuditInfo, "user", fmt.Sprintf("User approved command: %s", cmd))
		exitCode, output := e.runShell(cmd, sess.CWD)
		e.log(sess, AuditCmdResult, "engine", fmt.Sprintf("Exit Code: %d\x0a%s", exitCode, output))
		e.executePromptInternal(sess, output)
	})
}

func (e *Engine) ExecutePrompt(sess *Session, prompt string) {
	e.resumeAndRun(sess, func() {
		e.executePromptInternal(sess, prompt)
	})
}

func (e *Engine) resumeAndRun(sess *Session, f func()) {
	wasIntervention := sess.Status == StatusIntervention
	if sess.Status != StatusRunning {
		sess.Status = StatusRunning
		e.sm.Save(sess)
	}

	f()

	if wasIntervention {
		e.ResolveIntervention(sess.ID, "retry")
	}
}

func (e *Engine) executePromptInternal(sess *Session, prompt string) {
	e.log(sess, AuditLLMPrompt, "user", prompt)

	geminiSID := sess.RoleCache["default"]
	onChunk := e.onChunk(sess, &StateDef{SessionRole: "gemini"})
	resp, err := e.exec.Run(geminiSID, prompt, sess.CWD, sess.ApprovalMode, sess.Yolo, onChunk, func(newSID string) {
		sess.RoleCache["default"] = newSID
		e.sm.Save(sess)
	})
	onChunk("")

	if err != nil {
		e.log(sess, AuditInfo, "engine", "LLM Error: "+err.Error())
	} else {
		e.log(sess, AuditLLMResponse, "gemini", resp)
	}
}

func (e *Engine) resolveInstruction(instr, cwd string) string {
	if !strings.HasPrefix(instr, "@") {
		return instr
	}

	filename := strings.TrimPrefix(instr, "@")
	fullPath, err := e.sm.storage.ResolvePath(instr, e.sm.storage.BaseDir)
	if err != nil {
		return "Error: " + err.Error()
	}

	paths := []string{
		filepath.Join(e.sm.storage.BaseDir, "skills", filename),
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

func (e *Engine) runShell(cmdStr, cwd string) (int, string) {
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
			exitCode = 124 // Standard for timeout
			out = append(out, []byte("\nError: Command timed out after 30s")...)
		} else {
			exitCode = 1
		}
	}

	strOut := string(out)
	const maxLen = 32000
	if len(strOut) > maxLen {
		strOut = strOut[:1000] + "\x0a...[TRUNCATED]...\x0a" + strOut[len(strOut)-(maxLen-1100):]
	}
	return exitCode, strOut
}

func (e *Engine) log(sess *Session, eventType, source, content string) {
	e.sm.AppendAudit(sess, AuditEntry{
		Type:    eventType,
		Source:  source,
		Content: content,
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
