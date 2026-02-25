package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type CLI struct {
	Sm     *SessionManager
	Exec   *Executor
	Reg    *Registry
	Engine *Engine
	In     io.Reader
	Out    io.Writer
}

func NewCLI(sm *SessionManager, exec *Executor, reg *Registry, engine *Engine) *CLI {
	return &CLI{
		Sm:     sm,
		Exec:   exec,
		Reg:    reg,
		Engine: engine,
		In:     os.Stdin,
		Out:    os.Stdout,
	}
}

func (c *CLI) Run(resume bool) error {
	sess, err := c.initializeSession(resume)
	if err != nil {
		fmt.Fprintln(c.Out, "Error:", err)
		return nil
	}

	instanceID := fmt.Sprintf("cli-%d", os.Getpid())
	c.Reg.Set(instanceID, sess.ID)
	c.Reg.SetVerbosity(instanceID, "HIGH")

	fmt.Fprintf(c.Out, "Connected to session %s (Path: %s)\x0a", sess.ID, sess.CWD)
	fmt.Fprintln(c.Out, "Commands: /run <skill>, /last <N>, /intervene <action>")

	if resume && sess.SkillName != "" {
		c.resumeSkill(sess)
	}

	go c.listenEvents(sess.ID)

	return c.repl(sess)
}

func (c *CLI) initializeSession(resume bool) (*Session, error) {
	if resume {
		return c.Sm.GetLatest()
	}
	
	cwd, _ := os.Getwd()
	sess := &Session{
		ID:          uuid.New().String(),
		CWD:         cwd,
		LastUpdated: time.Now(),
		RoleCache:   make(map[string]string),
	}
	c.Sm.Save(sess)
	return sess, nil
}

func (c *CLI) resumeSkill(sess *Session) {
	skill, err := LoadSkill(c.Sm.StoragePath, sess.SkillName)
	if err == nil {
		if sess.Status != StatusRunning && sess.Status != StatusIntervention {
			fmt.Fprintf(c.Out, "Resuming task: %s (Skill: %s)\x0a", sess.ID, sess.SkillName)
			sess.Status = StatusRunning
			c.Sm.Save(sess)
		}
		go c.Engine.Run(skill, sess)
	}
}

func (c *CLI) listenEvents(sessionID string) {
	eventCh := GlobalBus.Subscribe()
	formatter := &AnsiFormatter{}
	
	for e := range eventCh {
		if e.SessionID == sessionID && e.Type == EventAudit {
			audit := e.Payload.(AuditEntry)
			if audit.Type == AuditLLMChunk {
				fmt.Fprint(c.Out, audit.Content)
				continue
			}

			fmt.Fprintf(c.Out, "\x0a%s\x0a", formatter.Format(audit))
			if audit.Type == AuditIntervention {
				fmt.Fprint(c.Out, "\x0aType `/intervene <retry|proceed_to_fail|abort>`\x0a> ")
			}
		}
	}
}

func (c *CLI) repl(sess *Session) error {
	scanner := bufio.NewScanner(c.In)
	for {
		fmt.Fprint(c.Out, "\x0a> ")
		if !scanner.Scan() {
			return io.EOF
		}
		text := scanner.Text()
		if text == "" {
			continue
		}

		parts := strings.Fields(text)
		cmd := parts[0]

		switch cmd {
		case "/run":
			if len(parts) > 1 {
				c.handleRun(sess, parts[1])
			}
		case "/last":
			n := 5
			if len(parts) > 1 { fmt.Sscanf(parts[1], "%d", &n) }
			c.handleLast(sess, n)
		case "/intervene":
			if len(parts) > 1 {
				c.Engine.ResolveIntervention(sess.ID, parts[1])
			}
		default:
			go c.Engine.ExecutePrompt(sess, text)
		}
	}
}

func (c *CLI) handleRun(sess *Session, skillName string) {
	skill, err := LoadSkill(c.Sm.StoragePath, skillName)
	if err != nil {
		fmt.Fprintln(c.Out, "Skill error:", err)
		return
	}
	sess.SkillName = skillName
	c.Sm.Save(sess)
	go c.Engine.Run(skill, sess)
}

func (c *CLI) handleLast(sess *Session, n int) {
	logs, _ := c.Sm.GetLastAudit(sess, n)
	formatter := &AnsiFormatter{}
	for _, l := range logs {
		fmt.Fprintln(c.Out, formatter.Format(l))
	}
}
