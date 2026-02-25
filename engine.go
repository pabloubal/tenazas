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

	if sess.ActiveNode == "" {
		sess.ActiveNode = skill.InitialState
		sess.Status = "running"
		sess.LoopCount = 0
		e.sm.Save(sess)
		e.log(sess, "info", "engine", fmt.Sprintf("Started skill %s at node %s", skill.Name, sess.ActiveNode))
	} else if sess.Status == "running" && sess.PendingFeedback == "" {
		sess.PendingFeedback = "Session resumed. Please continue from where you left off."
		e.sm.Save(sess)
	}

	for sess.Status == "running" || sess.Status == "intervention_required" {
		state, ok := skill.States[sess.ActiveNode]
		if !ok {
			sess.Status = "failed"
			e.log(sess, "info", "engine", "State "+sess.ActiveNode+" not found in skill")
			e.sm.Save(sess)
			break
		}

		if state.Type == "end" {
			sess.Status = "completed"
			e.log(sess, "info", "engine", "Skill completed successfully")
			e.sm.Save(sess)
			break
		}

		if sess.Status == "intervention_required" {
			e.log(sess, "intervention", "engine", fmt.Sprintf("Waiting for intervention at state %s. Options: retry, proceed_to_fail, abort", sess.ActiveNode))
			ch := e.getInterventionChan(sess.ID)
			action := <-ch
			
			switch action {
			case "retry":
				sess.RetryCount = 0
				sess.Status = "running"
			case "proceed_to_fail":
				sess.RetryCount = 0
				sess.LoopCount = 0 // Reset loop count after intervention
				sess.Status = "running"
				
				// Apply failure prompt as if we just failed
				exitCode, output := 1, "User manually triggered fail route"
				feedback := strings.ReplaceAll(state.OnFailPrompt, "{{exit_code}}", fmt.Sprintf("%d", exitCode))
				feedback = strings.ReplaceAll(feedback, "{{stderr}}", output)
				
				e.transitionToFailRoute(skill, &state, sess, feedback)
			case "abort":
				sess.Status = "failed"
			}
			e.sm.Save(sess)
			if sess.Status != "running" {
				continue
			}
			if action == "proceed_to_fail" {
				continue 
			}
		}

		if state.Type == "action_loop" {
			err := e.executeActionLoop(skill, &state, sess)
			if err != nil {
				e.log(sess, "info", "engine", "Action loop error: "+err.Error())
				sess.Status = "failed"
				e.sm.Save(sess)
				break
			}
		} else if state.Type == "tool" {
			e.executeTool(&state, sess)
		} else {
			e.log(sess, "info", "engine", "Unknown state type: "+state.Type)
			sess.Status = "failed"
			e.sm.Save(sess)
			break
		}
	}
}

func (e *Engine) executeActionLoop(skill *SkillGraph, state *StateDef, sess *Session) error {
	// 1. Pre-Action
	if state.PreActionCmd != "" && sess.RetryCount == 0 {
		e.log(sess, "info", "engine", "Running pre_action_cmd: "+state.PreActionCmd)
		exitCode, output := e.runShell(state.PreActionCmd, sess.CWD)
		if exitCode != 0 {
			e.log(sess, "cmd_result", "engine", fmt.Sprintf("pre_action_cmd failed (Exit: %d): %s", exitCode, output))
			// Technical failure -> Retry this node
			sess.RetryCount++
			sess.PendingFeedback = fmt.Sprintf("Pre-action command failed (Exit %d):\n%s", exitCode, output)
			if sess.RetryCount >= state.MaxRetries && state.MaxRetries > 0 {
				sess.Status = "intervention_required"
			}
			return nil
		}
	}

	// 2. LLM Execution
	geminiSID := sess.RoleCache[state.SessionRole]
	instruction := e.resolveInstruction(state.Instruction, sess.CWD)
	
	var prompt string
	if sess.PendingFeedback == "Session resumed. Please continue from where you left off." && geminiSID != "" {
		// Just a nudge for continuity since the LLM already has the instruction in history
		prompt = sess.PendingFeedback
		sess.PendingFeedback = "" 
	} else if sess.PendingFeedback != "" {
		// Logical failure or technical retry -> Send full context + feedback
		prompt = fmt.Sprintf("%s\n\n### FEEDBACK FROM PREVIOUS ATTEMPT:\n%s", instruction, sess.PendingFeedback)
		sess.PendingFeedback = ""
	} else {
		// Fresh start for this node
		prompt = instruction
	}
	
	e.log(sess, "info", "engine", fmt.Sprintf("Executing LLM (%s) with approval mode: %s", state.SessionRole, state.ApprovalMode))
	e.log(sess, "llm_prompt", state.SessionRole, prompt)

	response, err := e.exec.Run(geminiSID, prompt, sess.CWD, state.ApprovalMode, sess.Yolo, func(chunk string) {
		// Broadcast live chunk to listeners (CLI/TG)
		GlobalBus.Publish(Event{
			Type:      EventAudit,
			SessionID: sess.ID,
			Payload: AuditEntry{
				Type:    "llm_response_chunk",
				Source:  state.SessionRole,
				Content: chunk,
			},
		})
	}, func(newSID string) {
		sess.RoleCache[state.SessionRole] = newSID
		e.sm.Save(sess)
	})

	if err != nil {
		// Execution error -> Retry this node
		sess.RetryCount++
		sess.PendingFeedback = "Gemini execution error: " + err.Error()
		if sess.RetryCount >= state.MaxRetries && state.MaxRetries > 0 {
			sess.Status = "intervention_required"
		}
		e.sm.Save(sess)
		return nil // We don't return err to avoid failing the whole engine loop
	}
	e.log(sess, "llm_response", state.SessionRole, response)

	// 3. Verification
	if state.VerifyCmd != "" {
		e.log(sess, "info", "engine", "Running verify_cmd: "+state.VerifyCmd)
		exitCode, output := e.runShell(state.VerifyCmd, sess.CWD)
		e.log(sess, "cmd_result", "engine", fmt.Sprintf("Exit Code: %d\x0aOutput: %s", exitCode, output))

		if exitCode == 0 {
			// Success
			if state.PostActionCmd != "" {
				e.runShell(state.PostActionCmd, sess.CWD)
			}
			sess.RetryCount = 0
			sess.LoopCount = 0
			sess.PendingFeedback = ""
			sess.ActiveNode = state.Next
			e.sm.Save(sess)
			return nil
		}

		// Failure (Verification) -> Always follows OnFailRoute (Logical Cycle)
		maxLoops := e.maxLoops
		if skill.MaxLoops > 0 {
			maxLoops = skill.MaxLoops
		}

		sess.LoopCount++
		feedback := strings.ReplaceAll(state.OnFailPrompt, "{{exit_code}}", fmt.Sprintf("%d", exitCode))
		feedback = strings.ReplaceAll(feedback, "{{stderr}}", output)
		
		if sess.LoopCount >= maxLoops {
			sess.PendingFeedback = feedback
			sess.Status = "intervention_required"
		} else {
			e.transitionToFailRoute(skill, state, sess, feedback)
		}
		e.sm.Save(sess)
		return nil
	}

	// If no verification, assume success
	if state.PostActionCmd != "" {
		e.runShell(state.PostActionCmd, sess.CWD)
	}
	sess.RetryCount = 0
	sess.LoopCount = 0
	sess.ActiveNode = state.Next
	e.sm.Save(sess)
	return nil
}

func (e *Engine) transitionToFailRoute(skill *SkillGraph, state *StateDef, sess *Session, feedback string) {
	if state.OnFailRoute == "" {
		sess.Status = "failed"
		e.log(sess, "info", "engine", "No on_fail_route defined for state "+sess.ActiveNode)
		return
	}

	e.log(sess, "info", "engine", fmt.Sprintf("Transitioning to fail route: %s (Loop: %d)", state.OnFailRoute, sess.LoopCount))
	
	// Store feedback so the next node receives it in its prompt
	sess.PendingFeedback = feedback
	sess.ActiveNode = state.OnFailRoute
	sess.RetryCount = 0
}

func (e *Engine) executeTool(state *StateDef, sess *Session) {
	e.log(sess, "info", "engine", "Executing tool: "+state.Command)
	exitCode, out := e.runShell(state.Command, sess.CWD)
	e.log(sess, "cmd_result", "engine", fmt.Sprintf("Exit Code: %d\x0aOutput: %s", exitCode, out))
	
	sess.RetryCount = 0
	sess.ActiveNode = state.Next
	e.sm.Save(sess)
}

func (e *Engine) ExecutePrompt(sess *Session, prompt string) {
	e.log(sess, "llm_prompt", "user", prompt)

	// Use 'default' role for raw prompts
	geminiSID := sess.RoleCache["default"]

	_, err := e.exec.Run(geminiSID, prompt, sess.CWD, "", sess.Yolo, func(chunk string) {
		GlobalBus.Publish(Event{
			Type:      EventAudit,
			SessionID: sess.ID,
			Payload: AuditEntry{
				Type:    "llm_response_chunk",
				Source:  "gemini",
				Content: chunk,
			},
		})
	}, func(newSID string) {
		sess.RoleCache["default"] = newSID
		e.sm.Save(sess)
	})

	if err != nil {
		e.log(sess, "info", "engine", "LLM Error: "+err.Error())
	}
}

func (e *Engine) resolveInstruction(instr, cwd string) string {
	if !strings.HasPrefix(instr, "@") {
		return instr
	}

	filename := strings.TrimPrefix(instr, "@")
	// Try skill directory first
	path := filepath.Join(e.sm.StoragePath, "skills", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		// Fallback to project CWD
		path = filepath.Join(cwd, filename)
		data, err = os.ReadFile(path)
		if err != nil {
			return "Error: Could not load instruction file " + filename
		}
	}

	content := string(data)
	// Strip YAML frontmatter if present (lines between the first two ---)
	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content, "---", 3)
		if len(parts) == 3 {
			content = strings.TrimSpace(parts[2])
		}
	}

	return content
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
	if len(strOut) > 4000 {
		strOut = "...[TRUNCATED]...\x0a" + strOut[len(strOut)-3900:]
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
