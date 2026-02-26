package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
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
	sess   *Session
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

const (
	escSaveCursor    = "\x1b[s"
	escRestoreCursor = "\x1b[u"
	escClearLine     = "\x1b[2K"
	escClear         = "\x1b[2J\x1b[H"
	escReset         = "\x1b[0m"
	escBlueWhite     = "\x1b[44;37m"
)

func (c *CLI) Run(resume bool) error {
	sess, err := c.initializeSession(resume)
	if err != nil {
		fmt.Fprintln(c.Out, "Error:", err)
		return nil
	}
	c.sess = sess

	instanceID := fmt.Sprintf("cli-%d", os.Getpid())
	c.Reg.Set(instanceID, sess.ID)
	c.Reg.SetVerbosity(instanceID, "HIGH")

	c.writeEscape(escClear)
	c.setupTerminal()
	defer c.writeEscape("\x1b[r") // Restore scrolling region

	// Handle terminal resize
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH)
	go func() {
		for range sigChan {
			c.setupTerminal()
		}
	}()

	fmt.Fprintf(c.Out, "Connected to session %s (Path: %s)\x0a", sess.ID, sess.CWD)
	fmt.Fprintln(c.Out, "Commands: /run <skill>, /last <N>, /intervene <action>, /mode <plan|auto_edit|yolo>")

	if resume && sess.SkillName != "" {
		c.resumeSkill(sess)
	}

	go c.listenEvents(sess.ID)

	return c.repl(sess)
}

func (c *CLI) initializeSession(resume bool) (*Session, error) {
	if resume {
		sess, err := c.Sm.GetLatest()
		if err == nil && sess.ApprovalMode == "" {
			sess.ApprovalMode = ApprovalModePlan
		}
		return sess, err
	}
	
	cwd, _ := os.Getwd()
	sess := &Session{
		ID:           uuid.New().String(),
		CWD:          cwd,
		LastUpdated:  time.Now(),
		RoleCache:    make(map[string]string),
		ApprovalMode: ApprovalModePlan,
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
		case "/skills":
			c.handleSkills(parts[1:])
		case "/mode":
			c.handleMode(sess, parts[1:])
		default:
			go c.Engine.ExecutePrompt(sess, text)
		}
	}
}

func (c *CLI) handleSkills(args []string) {
	c.Sm.RefreshSkillRegistry()
	if len(args) >= 2 && args[0] == "toggle" {
		name := args[1]
		active, _ := c.Sm.GetActiveSkills()
		enabled := false
		for _, s := range active {
			if s == name {
				enabled = true
				break
			}
		}
		c.Sm.ToggleSkill(name, !enabled)
		return
	}

	// List skills
	all, _ := ListSkills(c.Sm.StoragePath)
	active, _ := c.Sm.GetActiveSkills()

	activeMap := make(map[string]bool)
	for _, s := range active {
		activeMap[s] = true
	}

	fmt.Fprintln(c.Out, "STATUS  NAME")
	for _, s := range all {
		status := "[ ]"
		if activeMap[s] {
			status = "[X]"
		}
		fmt.Fprintf(c.Out, "%-7s %s\x0a", status, s)
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

func formatFooter(mode string, yolo bool, skillCount int, sessionID string) string {
	m := mode
	if yolo {
		m = ApprovalModeYolo
	} else if m == "" {
		m = ApprovalModePlan
	}
	
	shortID := sessionID
	if len(sessionID) > 8 {
		shortID = sessionID[len(sessionID)-8:]
	}
	
	return fmt.Sprintf("[%s] | Skills: %d | Session: ...%s", m, skillCount, shortID)
}

func (c *CLI) handleMode(sess *Session, args []string) {
	if len(args) == 0 {
		fmt.Fprintf(c.Out, "Current mode: %s (Yolo: %v)\x0a", sess.ApprovalMode, sess.Yolo)
		return
	}

	mode := strings.ToUpper(args[0])
	switch mode {
	case ApprovalModeYolo:
		sess.Yolo = true
	case ApprovalModePlan, ApprovalModeAutoEdit:
		sess.Yolo = false
		sess.ApprovalMode = mode
	default:
		fmt.Fprintf(c.Out, "Invalid mode: %s. Use plan, auto_edit, or yolo.\x0a", args[0])
		return
	}
	if c.Sm != nil {
		c.Sm.Save(sess)
	}
	c.drawFooter(sess)
}

func (c *CLI) drawFooter(sess *Session) {
	if sess == nil {
		return
	}
	rows, cols, err := getTerminalSize()
	if err != nil {
		rows, cols = 24, 80
	}

	skillCount := 0
	if c.Sm != nil {
		skills, _ := ListSkills(c.Sm.StoragePath)
		skillCount = len(skills)
	}
	footer := formatFooter(sess.ApprovalMode, sess.Yolo, skillCount, sess.ID)

	// Pad footer to terminal width
	padding := ""
	if len(footer) < cols {
		padding = strings.Repeat(" ", cols-len(footer))
	} else if len(footer) > cols {
		footer = footer[:cols]
	}

	c.writeEscape(fmt.Sprintf("%s\x1b[%d;1H%s%s%s%s%s%s", escSaveCursor, rows, escClearLine, escBlueWhite, footer, padding, escReset, escRestoreCursor))
}

func (c *CLI) setupTerminal() {
	rows, _, err := getTerminalSize()
	if err != nil {
		rows = 24
	}

	// Set scrolling region: 1 to rows-1
	c.writeEscape(fmt.Sprintf("\x1b[1;%dr", rows-1))
	if c.sess != nil {
		c.drawFooter(c.sess)
	}
}

func (c *CLI) writeEscape(seq string) {
	fmt.Fprint(c.Out, seq)
}
