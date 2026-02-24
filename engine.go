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
	intervs    map[string]chan string
	intervsMux sync.RWMutex
}

func NewEngine(sm *SessionManager, exec *Executor) *Engine {
	return &Engine{
		sm:      sm,
		exec:    exec,
		intervs: make(map[string]chan string),
	}
}

func (e *Engine) Run(skill *SkillGraph, sess *Session) {
	if sess.ActiveNode == "" {
		sess.ActiveNode = skill.InitialState
		sess.Status = "running"
		e.sm.Save(sess)
		e.log(sess, "info", "engine", fmt.Sprintf("Started skill %s at node %s", skill.Name, sess.ActiveNode))
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
			e.log(sess, "intervention", "engine", fmt.Sprintf("Waiting for intervention at state %s. Options: retry, fail_route, abort", sess.ActiveNode))
			ch := e.getInterventionChan(sess.ID)
			action := <-ch
			
			switch action {
			case "retry":
				sess.RetryCount = 0
				sess.Status = "running"
			case "fail_route":
				sess.RetryCount = 0
				sess.Status = "running"
				sess.ActiveNode = state.OnFailRoute
			case "abort":
				sess.Status = "failed"
			}
			e.sm.Save(sess)
			if sess.Status != "running" {
				continue
			}
			if action == "fail_route" {
				continue // start next loop iteration with new node
			}
		}

		if state.Type == "action_loop" {
			err := e.executeActionLoop(&state, sess)
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

func (e *Engine) executeActionLoop(state *StateDef, sess *Session) error {
	// 1. Pre-Action
	if state.PreActionCmd != "" && sess.RetryCount == 0 {
		e.log(sess, "info", "engine", "Running pre_action_cmd: "+state.PreActionCmd)
		exitCode, output := e.runShell(state.PreActionCmd, sess.CWD)
		if exitCode != 0 {
			e.log(sess, "cmd_result", "engine", fmt.Sprintf("pre_action_cmd failed (Exit: %d): %s", exitCode, output))
			// Immediately jump to fail route
			sess.ActiveNode = state.OnFailRoute
			e.sm.Save(sess)
			return nil
		}
	}

	// 2. LLM Execution
	geminiSID := sess.RoleCache[state.SessionRole]
	prompt := e.resolveInstruction(state.Instruction, sess.CWD)
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
		return err
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
			sess.ActiveNode = state.Next
			e.sm.Save(sess)
			return nil
		}

		// Failure
		sess.RetryCount++
		if sess.RetryCount >= state.MaxRetries {
			sess.Status = "intervention_required"
		} else {
			// Format feedback
			feedback := strings.ReplaceAll(state.OnFailPrompt, "{{exit_code}}", fmt.Sprintf("%d", exitCode))
			feedback = strings.ReplaceAll(feedback, "{{stderr}}", output)
			state.Instruction = feedback
		}
		e.sm.Save(sess)
		return nil
	}

	// If no verification, assume success
	if state.PostActionCmd != "" {
		e.runShell(state.PostActionCmd, sess.CWD)
	}
	sess.RetryCount = 0
	sess.ActiveNode = state.Next
	e.sm.Save(sess)
	return nil
}

func (e *Engine) executeTool(state *StateDef, sess *Session) {
	e.log(sess, "info", "engine", "Executing tool: "+state.Command)
	exitCode, out := e.runShell(state.Command, sess.CWD)
	e.log(sess, "cmd_result", "engine", fmt.Sprintf("Exit Code: %d\x0aOutput: %s", exitCode, out))
	
	sess.RetryCount = 0
	sess.ActiveNode = state.Next
	e.sm.Save(sess)
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
	return string(data)
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
