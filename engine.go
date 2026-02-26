package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
}

func (e *Engine) awaitIntervention(skill *SkillGraph, state *StateDef, sess *Session) {
	e.log(sess, AuditIntervention, "engine", fmt.Sprintf("Waiting for intervention at %s", sess.ActiveNode))
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

func (e *Engine) executeActionLoop(skill *SkillGraph, state *StateDef, sess *Session) error {
	// Pre-Action
	if state.PreActionCmd != "" && sess.RetryCount == 0 {
		if exitCode, output := e.runShell(state.PreActionCmd, sess.CWD); exitCode != 0 {
			e.log(sess, AuditCmdResult, "engine", fmt.Sprintf("pre_action_cmd failed (Exit Code: %d): %s", exitCode, output))
			e.handleRetry(state, sess, fmt.Sprintf("Pre-action command failed (Exit Code: %d):\n%s", exitCode, output))
			return nil
		}
	}

	// LLM Step
	response, err := e.callLLM(state, sess)
	if err != nil {
		e.handleRetry(state, sess, "Gemini execution error: "+err.Error())
		return nil
	}
	sess.PendingFeedback = "" // Clear feedback only after successful LLM consumption
	e.log(sess, AuditLLMResponse, state.SessionRole, response)

	// Post-Action / Verification
	if state.VerifyCmd == "" {
		e.completeState(state, sess, "")
		return nil
	}

	exitCode, output := e.runShell(state.VerifyCmd, sess.CWD)
	e.log(sess, AuditCmdResult, "engine", fmt.Sprintf("Verification Result (Exit Code: %d):\x0a%s", exitCode, output))

	if exitCode == 0 {
		e.completeState(state, sess, output)
	} else {
		e.handleLoopFailure(skill, state, sess, exitCode, output)
	}
	return nil
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
	feedback := strings.ReplaceAll(state.OnFailPrompt, "{{exit_code}}", fmt.Sprintf("%d", exitCode))
	feedback = strings.ReplaceAll(feedback, "{{stderr}}", output)
	feedback = strings.ReplaceAll(feedback, "{{stdout}}", output)
	feedback = strings.ReplaceAll(feedback, "{{output}}", output)
	
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
		e.terminate(sess, StatusFailed, "No on_fail_route for state "+sess.ActiveNode)
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
		onText:    func(t string) { e.sm.AppendAudit(sess, AuditEntry{Type: AuditLLMChunk, Source: state.SessionRole, Content: t}) },
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
		targetTag, emit := "<thought>", p.onText
		if p.inThought {
			targetTag, emit = "</thought>", p.onThought
		}

		idx := strings.Index(p.buffer, "<")
		if idx == -1 {
			emit(p.buffer)
			p.buffer = ""
			return
		}

		if idx > 0 {
			emit(p.buffer[:idx])
			p.buffer = p.buffer[idx:]
		}

		// Buffer now starts with "<"
		if strings.HasPrefix(p.buffer, targetTag) {
			p.buffer = p.buffer[len(targetTag):]
			p.inThought = !p.inThought
			continue
		}

		if strings.HasPrefix(targetTag, p.buffer) {
			return
		}

		emit(p.buffer[:1])
		p.buffer = p.buffer[1:]
	}
}

func (p *thoughtParser) flush() {
	if p.buffer == "" {
		return
	}
	if p.inThought {
		p.onThought(p.buffer)
	} else {
		p.onText(p.buffer)
	}
	p.buffer = ""
}

func (e *Engine) onSID(sess *Session, state *StateDef) func(string) {
	return func(sid string) {
		sess.RoleCache[state.SessionRole] = sid
		e.sm.Save(sess)
	}
}

func (e *Engine) executeTool(state *StateDef, sess *Session) {
	e.log(sess, AuditInfo, "engine", "Executing tool: "+state.Command)
	exitCode, out := e.runShell(state.Command, sess.CWD)
	e.log(sess, AuditCmdResult, "engine", fmt.Sprintf("Exit Code: %d\x0aOutput: %s", exitCode, out))
	
	sess.RetryCount = 0
	sess.PendingFeedback = out
	sess.ActiveNode = state.Next
	e.sm.Save(sess)
}

func (e *Engine) ExecutePrompt(sess *Session, prompt string) {
	e.log(sess, AuditLLMPrompt, "user", prompt)

	geminiSID := sess.RoleCache["default"]
	onChunk := e.onChunk(sess, &StateDef{SessionRole: "gemini"})
	_, err := e.exec.Run(geminiSID, prompt, sess.CWD, sess.ApprovalMode, sess.Yolo, onChunk, func(newSID string) {
		sess.RoleCache["default"] = newSID
		e.sm.Save(sess)
	})
	onChunk("")

	if err != nil {
		e.log(sess, AuditInfo, "engine", "LLM Error: "+err.Error())
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
	cmd := exec.Command("bash", "-c", cmdStr)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = 1
		}
	}

	strOut := string(out)
	const maxLen = 32000
	if len(strOut) > maxLen {
		// Keep a bit of the beginning and the end
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
		ch = make(chan string)
		e.intervs[sessID] = ch
	}
	return ch
}

func (e *Engine) ResolveIntervention(sessID, action string) {
	e.intervsMux.RLock()
	ch, ok := e.intervs[sessID]
	e.intervsMux.RUnlock()
	
	if ok {
		ch <- action
	}
}
