package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/google/uuid"
)

type CLI struct {
	Sm            *SessionManager
	Exec          *Executor
	Reg           *Registry
	Engine        *Engine
	In            io.Reader
	Out           io.Writer
	sess          *Session
	input         []rune
	cursorPos     int
	completions   []string
	completionIdx int
	inRawMode     bool
	oldTermState  interface{}
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
	escCyan          = "\x1b[36m"
	escBoldCyan      = "\x1b[1;36m"
	escDim           = "\x1b[2m"
)

const asciiBanner = `
 ████████╗███████╗███╗   ██╗ █████╗ ███████╗ █████╗ ███████╗
 ╚══██╔══╝██╔════╝████╗  ██║██╔══██╗╚══███╔╝██╔══██╗██╔════╝
    ██║   █████╗  ██╔██╗ ██║███████║  ███╔╝ ███████║███████╗
    ██║   ██╔══╝  ██║╚██╗██║██╔══██║ ███╔╝  ██╔══██║╚════██║
    ██║   ███████╗██║ ╚████║██║  ██║███████╗██║  ██║███████║
    ╚═╝   ╚══════╝╚═╝  ╚═══╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚══════╝
`

func (c *CLI) writeColor(color, text string) {
	fmt.Fprint(c.Out, color, text, escReset)
}

func (c *CLI) drawBranding() {
	lines := strings.Split(strings.Trim(asciiBanner, "\x0a"), "\x0a")
	// Gradient from Blue (DeepSkyBlue1) to Cyan (Cyan1)
	// 256-color palette: 33, 39, 45, 51
	colors := []string{
		"\x1b[38;5;33m",
		"\x1b[38;5;39m",
		"\x1b[38;5;45m",
		"\x1b[38;5;45m",
		"\x1b[38;5;51m",
		"\x1b[38;5;51m",
	}

	for i, line := range lines {
		color := colors[0]
		if i < len(colors) {
			color = colors[i]
		}
		// Prefix each line with a Gemini-style ">" and apply gradient
		fmt.Fprintf(c.Out, "\x1b[1;34m> \x1b[0m%s%s%s\x0a", color, line, escReset)
	}
	fmt.Fprintln(c.Out)

	// Print Session Info in Dimmed Gray
	c.writeColor(escDim, fmt.Sprintf("Session: %s\x0a", c.sess.ID))
	c.writeColor(escDim, fmt.Sprintf("Path:    %s\x0a\x0a", c.sess.CWD))
}

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

	// Insert branding here
	c.drawBranding()

	// Handle terminal resize
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH)
	go func() {
		for range sigChan {
			c.setupTerminal()
		}
	}()

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

func (c *CLI) resetCompletions() {
	c.completions = nil
	c.completionIdx = -1
}

func (c *CLI) getCompletions(line string) []string {
	if !strings.HasPrefix(line, "/") {
		return []string{}
	}

	commands := []string{"/run", "/last", "/intervene", "/skills", "/mode", "/help"}

	if strings.HasPrefix(line, "/run ") {
		prefix := strings.TrimPrefix(line, "/run ")
		skills, err := ListSkills(c.Sm.StoragePath)
		if err != nil {
			return []string{}
		}
		matches := []string{}
		for _, s := range skills {
			if strings.HasPrefix(s, prefix) {
				matches = append(matches, "/run "+s)
			}
		}
		sort.Strings(matches)
		return matches
	}

	matches := []string{}
	for _, cmd := range commands {
		if strings.HasPrefix(cmd, line) {
			matches = append(matches, cmd)
		}
	}
	// Note: We don't sort matches here to preserve the order expected by tests
	return matches
}

func (c *CLI) getDimmedSuggestion(line string) string {
	if len(c.completions) != 1 {
		return ""
	}
	suggestion := c.completions[0]
	if strings.HasPrefix(suggestion, line) {
		return suggestion[len(line):]
	}
	return ""
}

func (c *CLI) handleTab() {
	if len(c.completions) == 0 {
		c.completions = c.getCompletions(string(c.input))
		if len(c.completions) == 0 {
			return
		}
		c.completionIdx = 0
	} else {
		c.completionIdx = (c.completionIdx + 1) % len(c.completions)
	}

	c.input = []rune(c.completions[c.completionIdx])
	c.cursorPos = len(c.input)
}

func (c *CLI) handleRune(r rune) {
	// Insert at cursorPos
	c.input = append(c.input[:c.cursorPos], append([]rune{r}, c.input[c.cursorPos:]...)...)
	c.cursorPos++
	c.resetCompletions()
}

func (c *CLI) handleBackspace() {
	if c.cursorPos > 0 {
		c.input = append(c.input[:c.cursorPos-1], c.input[c.cursorPos:]...)
		c.cursorPos--
		c.resetCompletions()
		if strings.HasPrefix(string(c.input), "/") {
			c.completions = c.getCompletions(string(c.input))
		}
	}
}

func (c *CLI) renderLine() {
	line := string(c.input)
	fmt.Fprintf(c.Out, "\r> %s", line)

	if suggestion := c.getDimmedSuggestion(line); suggestion != "" {
		c.writeColor(escDim, suggestion)
	}

	fmt.Fprint(c.Out, "\x1b[K")
	// Position cursor: prompt "> " is 2 characters
	fmt.Fprintf(c.Out, "\r\x1b[%dC", c.cursorPos+2)
}

func (c *CLI) handleCommand(sess *Session, text string) {
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := parts[0]

	switch cmd {
	case "/run":
		if len(parts) > 1 {
			c.handleRun(sess, parts[1])
		}
	case "/last":
		n := 5
		if len(parts) > 1 {
			fmt.Sscanf(parts[1], "%d", &n)
		}
		c.handleLast(sess, n)
	case "/intervene":
		if len(parts) > 1 {
			c.Engine.ResolveIntervention(sess.ID, parts[1])
		}
	case "/skills":
		c.handleSkills(parts[1:])
	case "/mode":
		c.handleMode(sess, parts[1:])
	case "/help":
		c.handleHelp()
	default:
		go c.Engine.ExecutePrompt(sess, text)
	}
}

func (c *CLI) repl(sess *Session) error {
	fd := int(syscall.Stdin)
	if oldState, err := enableRawMode(fd); err == nil {
		c.inRawMode = true
		c.oldTermState = oldState
		defer restoreTerminal(fd, oldState)
		return c.replRaw(sess)
	}

	// Fallback to basic scanner
	scanner := bufio.NewScanner(c.In)
	for {
		fmt.Fprint(c.Out, "\x0a> ")
		if !scanner.Scan() {
			return io.EOF
		}
		text := scanner.Text()
		if text != "" {
			c.handleCommand(sess, text)
		}
	}
}

func (c *CLI) replRaw(sess *Session) error {
	reader := bufio.NewReader(c.In)
	for {
		c.renderLine()
		r, _, err := reader.ReadRune()
		if err != nil {
			return err
		}

		switch r {
		case '\x03': // Ctrl-C
			return nil
		case '\r', '\n':
			line := string(c.input)
			fmt.Fprintln(c.Out)
			if line != "" {
				c.handleCommand(sess, line)
			}
			c.input = nil
			c.cursorPos = 0
			c.resetCompletions()
		case '\t':
			c.handleTab()
		case '\x7f', '\x08': // Backspace
			c.handleBackspace()
		case '\x1b':
			if reader.Buffered() > 0 {
				r2, _, _ := reader.ReadRune()
				if r2 == '[' {
					r3, _, _ := reader.ReadRune()
					switch r3 {
					case 'C': // Right
						if c.cursorPos < len(c.input) {
							c.cursorPos++
						}
					case 'D': // Left
						if c.cursorPos > 0 {
							c.cursorPos--
						}
					}
				}
			}
		default:
			if unicode.IsPrint(r) {
				c.handleRune(r)
				if strings.HasPrefix(string(c.input), "/") {
					c.completions = c.getCompletions(string(c.input))
				}
			}
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

func (c *CLI) handleHelp() {
	fmt.Fprintln(c.Out, "Commands:")
	fmt.Fprintln(c.Out, "  /run <skill>         Run a specific skill")
	fmt.Fprintln(c.Out, "  /last <N>            Show last N audit logs")
	fmt.Fprintln(c.Out, "  /intervene <action>  Resolve an intervention")
	fmt.Fprintln(c.Out, "  /skills              List or toggle skills")
	fmt.Fprintln(c.Out, "  /mode <mode>         Switch approval mode (plan, auto_edit, yolo)")
	fmt.Fprintln(c.Out, "  /help                Show this help")
	fmt.Fprintln(c.Out, "\x0aModes: plan, auto_edit, yolo")
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
