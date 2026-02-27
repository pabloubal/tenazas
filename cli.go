package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
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
	mu            sync.Mutex
	IsImmersive   bool
	drawer        []string
	lastTabTime   time.Time
	isThinking    bool
	pulseFrame    int
	lastThought   string // Accumulated CoT for the footer hint
	skillCount    int    // Cached count for footer
}

func (c *CLI) refreshSkillCount() {
	if c.Sm != nil {
		skills, _ := ListSkills(c.Sm.StoragePath)
		c.mu.Lock()
		c.skillCount = len(skills)
		c.mu.Unlock()
	}
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
	escHideCursor    = "\x1b[?25l"
	escShowCursor    = "\x1b[?25h"
	escScrollRegion  = "\x1b[%d;%dr"
	escMoveTo        = "\x1b[%d;1H"
	escMoveRight     = "\x1b[%dC"
	escClearToEOL    = "\x1b[K"
	escCR            = "\r"
)

const (
	drawerHeight      = 8
	doubleTabInterval = 300 * time.Millisecond
	promptNormal      = "‹ › "
	promptPulse       = "« » "
	promptOffset      = 4
)

func (c *CLI) getPrompt() string {
	if !c.isThinking {
		return promptNormal
	}
	if c.pulseFrame%2 == 1 {
		return promptPulse
	}
	return promptNormal
}

func (c *CLI) write(s string) {
	if c.Out == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeLocked(s)
}

func (c *CLI) writeLocked(s string) {
	fmt.Fprint(c.Out, s)
}

const asciiBanner = `
 ████████╗███████╗███╗   ██╗ █████╗ ███████╗ █████╗ ███████╗
 ╚══██╔══╝██╔════╝████╗  ██║██╔══██╗╚══███╔╝██╔══██╗██╔════╝
    ██║   █████╗  ██╔██╗ ██║███████║  ███╔╝ ███████║███████╗
    ██║   ██╔══╝  ██║╚██╗██║██╔══██║ ███╔╝  ██╔══██║╚════██║
    ██║   ███████╗██║ ╚████║██║  ██║███████╗██║  ██║███████║
    ╚═╝   ╚══════╝╚═╝  ╚═══╝╚═╝  ╚═╝╚══════╝╚═╝  ╚═╝╚══════╝
`

func (c *CLI) writeColor(color, text string) {
	c.write(color + text + escReset)
}

func (c *CLI) writeAtomic(sb *strings.Builder, fn func(*strings.Builder)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(sb)
	if c.Out != nil {
		c.writeLocked(sb.String())
	}
}

func (c *CLI) drawBrandingAtomic(sb *strings.Builder) {
	lines := strings.Split(strings.Trim(asciiBanner, "\x0a"), "\x0a")
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
		fmt.Fprintf(sb, "\x1b[1;34m%s\x1b[0m%s%s%s\x0a", promptNormal, color, line, escReset)
	}
	fmt.Fprintf(sb, "\x0a%s TENAZAS — This is the (Gate)way %s\x0a\x0a", escBoldCyan, escReset)

	if c.sess != nil {
		fmt.Fprintf(sb, "%sSession: %s%s\x0a", escDim, c.sess.ID, escReset)
		fmt.Fprintf(sb, "%sPath:    %s%s\x0a\x0a", escDim, c.sess.CWD, escReset)
	}
}

func (c *CLI) drawBranding() {
	var sb strings.Builder
	c.drawBrandingAtomic(&sb)
	c.write(sb.String())
}

func (c *CLI) Run(resume bool) error {
	sess, err := c.initializeSession(resume)
	if err != nil {
		c.write(fmt.Sprintln("Error:", err))
		return nil
	}
	c.sess = sess

	instanceID := fmt.Sprintf("cli-%d", os.Getpid())
	c.Reg.Set(instanceID, sess.ID)
	c.Reg.SetVerbosity(instanceID, "HIGH")

	c.writeEscape(escClear)
	c.setupTerminal()
	c.refreshSkillCount()
	defer c.writeEscape("\x1b[r") // Restore scrolling region

	// Insert branding here
	c.drawBranding()

	// Handle terminal resize
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH)
	go func() {
		for range sigChan {
			c.setupTerminal()
			c.mu.Lock()
			isImm := c.IsImmersive
			c.mu.Unlock()
			if isImm {
				c.drawDrawer()
				c.renderLine()
			}
		}
	}()

	c.write("Commands: /run <skill>, /last <N>, /intervene <action>, /mode <plan|auto_edit|yolo>\x0a")

	if resume && sess.SkillName != "" {
		c.resumeSkill(sess)
	}

	go c.listenEvents(sess.ID)
	go c.pulseLoop()

	return c.repl(sess)
}

func (c *CLI) pulseLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	for range ticker.C {
		c.mu.Lock()
		if !c.isThinking {
			c.mu.Unlock()
			continue
		}
		c.pulseFrame++
		c.mu.Unlock()
		c.renderLine()
	}
}

func (c *CLI) selectSession() (*Session, error) {
	fd := int(syscall.Stdin)
	oldState, err := enableRawMode(fd)
	if err != nil {
		return nil, fmt.Errorf("failed to enable raw mode: %v", err)
	}
	defer restoreTerminal(fd, oldState)

	page := 0
	pageSize := 10
	selectedIndex := 0

	for {
		sessions, total, err := c.Sm.ListActive(page, pageSize)
		if err != nil {
			return nil, fmt.Errorf("could not list sessions: %v", err)
		}
		if total == 0 {
			c.write("No sessions found to resume.\x0a")
			return nil, fmt.Errorf("no sessions to resume")
		}

		var sb strings.Builder
		sb.WriteString(escClear)
		sb.WriteString(escHideCursor)
		defer c.write(escShowCursor)

		totalPages := (total + pageSize - 1) / pageSize
		fmt.Fprintf(&sb, "Select a session to resume (Page %d/%d):\x0a\x0a", page+1, totalPages)

		for i, s := range sessions {
			cursor := "  "
			if i == selectedIndex {
				cursor = escBoldCyan + "› " + escReset
			}
			title := s.Title
			if title == "" {
				title = s.CWD
			}
			if len(title) > 60 {
				title = "..." + title[len(title)-57:]
			}

			ts := s.LastUpdated.Format("2006-01-02 15:04")
			skill := ""
			if s.SkillName != "" {
				skill = fmt.Sprintf("(%s)", s.SkillName)
			}
			fmt.Fprintf(&sb, "%s%-60s %s [%s] %s\x0a", cursor, title, ts, s.ID[:8], skill)
		}
		fmt.Fprintf(&sb, "\x0a  %sUse ↑/↓ to navigate, ←/→ for pages, Enter to select, q to quit.%s\x0a", escDim, escReset)
		c.write(sb.String())

		reader := bufio.NewReader(c.In)
		r, _, err := reader.ReadRune()
		if err != nil {
			return nil, err
		}

		switch r {
		case 'q', '\x03': // q or Ctrl-C
			return nil, fmt.Errorf("aborted")
		case '\r', '\n':
			if selectedIndex < len(sessions) {
				return &sessions[selectedIndex], nil
			}
		case '\x1b': // escape sequence
			if reader.Buffered() > 0 {
				r2, _, _ := reader.ReadRune()
				if r2 == '[' {
					r3, _, _ := reader.ReadRune()
					switch r3 {
					case 'A': // Up
						if selectedIndex > 0 {
							selectedIndex--
						}
					case 'B': // Down
						if selectedIndex < len(sessions)-1 {
							selectedIndex++
						}
					case 'C': // Right
						if (page+1)*pageSize < total {
							page++
							selectedIndex = 0
						}
					case 'D': // Left
						if page > 0 {
							page--
							selectedIndex = 0
						}
					}
				}
			}
		}
	}
}

func (c *CLI) initializeSession(resume bool) (*Session, error) {
	if resume {
		return c.selectSession()
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
			c.write(fmt.Sprintf("Resuming task: %s (Skill: %s)\x0a", sess.ID, sess.SkillName))
			sess.Status = StatusRunning
			c.Sm.Save(sess)
		}
		go c.Engine.Run(skill, sess)
	}
}

func (c *CLI) setThinking(thinking bool) {
	c.mu.Lock()
	changed := c.isThinking != thinking
	c.isThinking = thinking
	c.mu.Unlock()
	if changed {
		c.renderLine()
	}
}

func (c *CLI) listenEvents(sessionID string) {
	eventCh := GlobalBus.Subscribe()
	formatter := &AnsiFormatter{}

	for e := range eventCh {
		if e.SessionID == sessionID && e.Type == EventAudit {
			audit, ok := e.Payload.(AuditEntry)
			if !ok {
				continue
			}

			// Update thinking state
			if audit.Type == AuditLLMPrompt {
				c.mu.Lock()
				c.lastThought = ""
				c.mu.Unlock()
				c.setThinking(true)
			} else if audit.Type == AuditLLMChunk || audit.Type == AuditLLMThought {
				c.setThinking(false)
			}

			// Thoughts always go to the drawer
			if audit.Type == AuditLLMThought {
				c.mu.Lock()
				c.lastThought += audit.Content
				c.mu.Unlock()
				c.addThought(audit.Content)
				continue
			}

			// Chunks stream directly to main area
			if audit.Type == AuditLLMChunk {
				c.write(audit.Content)
				continue
			}

			// Skip printing full response if we already streamed chunks
			if audit.Type == AuditLLMResponse {
				continue
			}

			// Background logs go to drawer if immersive, or both if not
			if audit.Type == AuditCmdResult || audit.Type == AuditStatus || audit.Type == AuditInfo {
				formatted := formatter.Format(audit)
				c.addThought(formatted)
				c.mu.Lock()
				isImm := c.IsImmersive
				c.mu.Unlock()
				if isImm {
					continue
				}
			}

			c.write(fmt.Sprintf("\x0a%s\x0a", formatter.Format(audit)))
			if audit.Type == AuditIntervention {
				c.write("\x0aType `/intervene <retry|proceed_to_fail|abort>`\x0a" + promptNormal)
			}
		}
	}
}

func (c *CLI) resetCompletions() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetCompletionsLocked()
}

func (c *CLI) resetCompletionsLocked() {
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

func (c *CLI) redrawScreenLocked() {
	var sb strings.Builder
	sb.WriteString(escClear)
	c.setupTerminalAtomic(&sb)
	c.drawBrandingAtomic(&sb)
	c.redrawAllAtomic(&sb)
	c.writeLocked(sb.String())
}

func (c *CLI) toggleImmersive() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.IsImmersive = !c.IsImmersive
	c.redrawScreenLocked()
}

func (c *CLI) handleTab() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handleTabLocked(c.sess)
}

func (c *CLI) handleTabLocked(sess *Session) {
	now := time.Now()
	if !c.lastTabTime.IsZero() && now.Sub(c.lastTabTime) < doubleTabInterval {
		if c.shouldToggleImmersiveLocked(now) {
			c.IsImmersive = !c.IsImmersive
			c.lastTabTime = time.Time{}
			c.redrawScreenLocked()
			return
		}
	}
	c.lastTabTime = now
	c.cycleCompletionsLocked()
}

func (c *CLI) shouldToggleImmersiveLocked(now time.Time) bool {
	if !c.inRawMode && now.Sub(c.lastTabTime) <= 10*time.Millisecond {
		return false
	}
	if len(c.completions) > 0 && c.completionIdx >= 0 && c.completionIdx < len(c.completions) {
		return string(c.input) == c.completions[c.completionIdx]
	}
	return len(c.completions) == 0
}

func (c *CLI) cycleCompletions() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cycleCompletionsLocked()
}

func (c *CLI) cycleCompletionsLocked() {
	if len(c.completions) == 0 || (c.completionIdx >= 0 && c.completionIdx < len(c.completions) && string(c.input) != c.completions[c.completionIdx]) {
		c.completions = c.getCompletions(string(c.input))
		if len(c.completions) == 0 {
			c.completionIdx = -1
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
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handleRuneLocked(r)
}

func (c *CLI) handleRuneLocked(r rune) {
	// Insert at cursorPos
	c.input = append(c.input[:c.cursorPos], append([]rune{r}, c.input[c.cursorPos:]...)...)
	c.cursorPos++
	c.updateCompletionsLocked()
}

func (c *CLI) handleBackspace() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handleBackspaceLocked()
}

func (c *CLI) handleBackspaceLocked() {
	if c.cursorPos > 0 {
		c.input = append(c.input[:c.cursorPos-1], c.input[c.cursorPos:]...)
		c.cursorPos--
		c.updateCompletionsLocked()
	}
}

func (c *CLI) updateCompletionsLocked() {
	c.resetCompletionsLocked()
	if strings.HasPrefix(string(c.input), "/") {
		c.completions = c.getCompletions(string(c.input))
	}
}

func (c *CLI) renderLineAtomic(sb *strings.Builder) {
	if c.IsImmersive {
		rows, _ := c.getTermSize()
		fmt.Fprintf(sb, escMoveTo, c.promptRow(rows))
	}

	prompt := c.getPrompt()
	if c.isThinking {
		sb.WriteString(escCyan)
	}

	line := string(c.input)
	fmt.Fprintf(sb, "%s%s%s", escCR, prompt, line)

	if c.isThinking {
		sb.WriteString(escReset)
	}

	if suggestion := c.getDimmedSuggestion(line); suggestion != "" {
		fmt.Fprintf(sb, "%s%s%s", escDim, suggestion, escReset)
	}

	sb.WriteString(escClearToEOL)
	fmt.Fprintf(sb, escCR+escMoveRight, c.cursorPos+promptOffset)
}

func (c *CLI) renderLine() {
	var sb strings.Builder
	c.writeAtomic(&sb, func(sb *strings.Builder) {
		c.renderLineAtomic(sb)
	})
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
		c.write("\x0a" + promptNormal)
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
	c.inRawMode = true
	reader := bufio.NewReader(c.In)
	for {
		c.renderLine()
		r, _, err := reader.ReadRune()
		if err != nil {
			return err
		}

		c.mu.Lock()
		// Reset double-tab timer for any key except Tab
		if r != '\t' {
			c.lastTabTime = time.Time{}
		}

		switch r {
		case '\x03': // Ctrl-C
			c.mu.Unlock()
			return nil
		case '\r', '\n':
			line := string(c.input)
			c.input = nil
			c.cursorPos = 0
			c.resetCompletionsLocked()
			c.mu.Unlock()

			c.write("\x0a")
			if line != "" {
				c.handleCommand(sess, line)
			}
		case '\t':
			c.handleTabLocked(sess)
			c.mu.Unlock()
		case '\x7f', '\x08': // Backspace
			c.handleBackspaceLocked()
			c.mu.Unlock()
		case '\x1b':
			c.mu.Unlock()
			if reader.Buffered() > 0 {
				c.handleEscape(reader, sess)
			}
		default:
			if unicode.IsPrint(r) {
				c.handleRuneLocked(r)
			}
			c.mu.Unlock()
		}
	}
}

func (c *CLI) handleSkills(args []string) {
	c.Sm.RefreshSkillRegistry()
	defer c.refreshSkillCount()

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

	c.write("STATUS  NAME\x0a")
	for _, s := range all {
		status := "[ ]"
		if activeMap[s] {
			status = "[X]"
		}
		c.write(fmt.Sprintf("%-7s %s\x0a", status, s))
	}
}

func (c *CLI) handleRun(sess *Session, skillName string) {
	skill, err := LoadSkill(c.Sm.StoragePath, skillName)
	if err != nil {
		c.write(fmt.Sprintln("Skill error:", err))
		return
	}
	sess.SkillName = skillName
	c.Sm.Save(sess)
	go c.Engine.Run(skill, sess)
}

func (c *CLI) handleLast(sess *Session, n int) {
	logs, _ := c.Sm.GetLastAudit(sess, n)
	formatter := &AnsiFormatter{}
	var output strings.Builder
	for _, l := range logs {
		fmt.Fprintln(&output, formatter.Format(l))
	}
	c.write(output.String())
}

func formatFooter(mode string, yolo bool, skillCount int, hint string) string {
	displayMode := mode
	if yolo {
		displayMode = ApprovalModeYolo
	} else if displayMode == "" {
		displayMode = ApprovalModePlan
	}

	condensedHint := strings.Join(strings.Fields(hint), " ")
	return fmt.Sprintf("[%s] | Skills: %d | Thought: %s", displayMode, skillCount, condensedHint)
}

func (c *CLI) handleMode(sess *Session, args []string) {
	if len(args) == 0 {
		c.write(fmt.Sprintf("Current mode: %s (Yolo: %v)\x0a", sess.ApprovalMode, sess.Yolo))
		return
	}
	c.setApprovalMode(sess, args[0])
}

func (c *CLI) persistSession(sess *Session) {
	if c.Sm != nil {
		c.Sm.Save(sess)
	}
}

func (c *CLI) handleHelp() {
	var output strings.Builder
	fmt.Fprintln(&output, "Commands:")
	fmt.Fprintln(&output, "  /run <skill>         Run a specific skill")
	fmt.Fprintln(&output, "  /last <N>            Show last N audit logs")
	fmt.Fprintln(&output, "  /intervene <action>  Resolve an intervention")
	fmt.Fprintln(&output, "  /skills              List or toggle skills")
	fmt.Fprintln(&output, "  /mode <mode>         Switch approval mode (plan, auto_edit, yolo)")
	fmt.Fprintln(&output, "  /help                Show this help")
	fmt.Fprintln(&output, "\x0aModes: plan, auto_edit, yolo")
	c.write(output.String())
}

func (c *CLI) drawFooterAtomic(sb *strings.Builder, sess *Session) {
	if sess == nil {
		return
	}
	rows, cols := c.getTermSize()

	hint := c.lastThought
	skillCount := c.skillCount

	footer := formatFooter(sess.ApprovalMode, sess.Yolo, skillCount, hint)

	if len(footer) > cols {
		footer = footer[:cols]
	}

	sb.WriteString(escSaveCursor)
	fmt.Fprintf(sb, escMoveTo, rows)
	sb.WriteString(escClearLine)
	sb.WriteString(escBlueWhite)
	fmt.Fprintf(sb, "%-*s", cols, footer)
	sb.WriteString(escReset)
	sb.WriteString(escRestoreCursor)
}

func (c *CLI) handleEscape(reader *bufio.Reader, sess *Session) {
	r2, _, _ := reader.ReadRune()
	if r2 != '[' {
		return
	}
	r3, _, _ := reader.ReadRune()
	c.mu.Lock()
	defer c.mu.Unlock()
	switch r3 {
	case 'C': // Right
		if c.cursorPos < len(c.input) {
			c.cursorPos++
		}
	case 'D': // Left
		if c.cursorPos > 0 {
			c.cursorPos--
		}
	case 'Z': // Shift-Tab
		c.cycleModeLocked(sess)
	}
}

func (c *CLI) cycleMode(sess *Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cycleModeLocked(sess)
}

func (c *CLI) setApprovalMode(sess *Session, mode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setApprovalModeLocked(sess, mode)
}

func (c *CLI) setApprovalModeLocked(sess *Session, mode string) {
	mode = strings.ToUpper(mode)
	switch mode {
	case ApprovalModeYolo:
		sess.Yolo = true
		sess.ApprovalMode = ApprovalModeYolo
	case ApprovalModePlan, ApprovalModeAutoEdit:
		sess.Yolo = false
		sess.ApprovalMode = mode
	default:
		c.writeLocked(fmt.Sprintf("Invalid mode: %s. Use plan, auto_edit, or yolo.\x0a", mode))
		return
	}
	c.persistSession(sess)
	c.drawFooterLocked(sess)
}

func (c *CLI) cycleModeLocked(sess *Session) {
	newMode := ApprovalModePlan
	if sess.Yolo {
		newMode = ApprovalModePlan
	} else {
		switch sess.ApprovalMode {
		case ApprovalModePlan:
			newMode = ApprovalModeAutoEdit
		case ApprovalModeAutoEdit:
			newMode = ApprovalModeYolo
		default:
			newMode = ApprovalModePlan
		}
	}
	c.setApprovalModeLocked(sess, newMode)
}

func (c *CLI) drawFooter(sess *Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drawFooterLocked(sess)
}

func (c *CLI) drawFooterLocked(sess *Session) {
	var sb strings.Builder
	c.drawFooterAtomic(&sb, sess)
	c.writeLocked(sb.String())
}

func (c *CLI) getTermSize() (int, int) {
	rows, cols, err := getTerminalSize()
	if err != nil {
		return 24, 80
	}
	return rows, cols
}

func (c *CLI) drawerStartRow(rows int) int {
	return rows - drawerHeight
}

func (c *CLI) promptRow(rows int) int {
	return rows - drawerHeight - 1
}

func (c *CLI) setupTerminalAtomic(sb *strings.Builder) {
	rows, _ := c.getTermSize()

	bottomReserved := 1 // Just footer
	if c.IsImmersive {
		bottomReserved = drawerHeight + 2
	}

	fmt.Fprintf(sb, escScrollRegion, 1, rows-bottomReserved)
	if c.sess != nil {
		c.drawFooterAtomic(sb, c.sess)
	}
}

func (c *CLI) setupTerminal() {
	var sb strings.Builder
	c.writeAtomic(&sb, func(sb *strings.Builder) {
		c.setupTerminalAtomic(sb)
	})
}

func (c *CLI) addThought(text string) {
	var sb strings.Builder
	c.writeAtomic(&sb, func(sb *strings.Builder) {
		lines := strings.Split(text, "\x0a")
		for _, l := range lines {
			if l == "" {
				continue
			}
			c.drawer = append(c.drawer, l)
		}
		if len(c.drawer) > drawerHeight {
			c.drawer = c.drawer[len(c.drawer)-drawerHeight:]
		}
		c.redrawAllAtomic(sb)
	})
}

func (c *CLI) redrawAllAtomic(sb *strings.Builder) {
	if c.sess != nil {
		c.drawFooterAtomic(sb, c.sess)
	}
	c.drawDrawerAtomic(sb)
	c.renderLineAtomic(sb)
}

func (c *CLI) drawDrawerAtomic(sb *strings.Builder) {
	if !c.IsImmersive {
		return
	}

	rows, cols := c.getTermSize()
	sb.WriteString(escSaveCursor)

	startRow := c.drawerStartRow(rows)
	for i := 0; i < drawerHeight; i++ {
		row := startRow + i
		line := ""
		if i < len(c.drawer) {
			line = c.drawer[i]
		}

		if cols > 7 && len(line) > cols-4 {
			line = line[:cols-7] + "..."
		}

		fmt.Fprintf(sb, escMoveTo+escClearLine+escDim+"• %s"+escReset, row, line)
	}

	sb.WriteString(escRestoreCursor)
}

func (c *CLI) drawDrawer() {
	var sb strings.Builder
	c.drawDrawerAtomic(&sb)
	c.write(sb.String())
}

func (c *CLI) writeEscape(seq string) {
	c.write(seq)
}
