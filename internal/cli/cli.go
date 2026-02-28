package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/google/uuid"

	"tenazas/internal/engine"
	"tenazas/internal/events"
	"tenazas/internal/formatter"
	"tenazas/internal/models"
	"tenazas/internal/registry"
	"tenazas/internal/session"
	"tenazas/internal/skill"
)

type CLI struct {
	Sm            *session.Manager
	Reg           *registry.Registry
	Engine        *engine.Engine
	DefaultClient string
	In            io.Reader
	Out           io.Writer
	sess          *models.Session
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
	lastThought   string
	skillCount    int
	lastRows         int // tracks terminal rows for resize cleanup
	gitBranch        string
	lastRenderLines  int // tracks how many terminal rows the last input render occupied
	promptLines      int // current number of wrapped prompt lines (for footer positioning)
	lastEscTime      time.Time
	isStreaming       bool // true while engine is producing output; keeps cursor in scroll region
}

func (c *CLI) refreshSkillCount() {
	if c.Sm != nil {
		skills, _ := skill.List(c.Sm.StoragePath)
		c.mu.Lock()
		c.skillCount = len(skills)
		c.mu.Unlock()
	}
}

func (c *CLI) refreshGitBranch() {
	if c.sess == nil {
		return
	}
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = c.sess.CWD
	out, err := cmd.Output()
	if err != nil {
		return
	}
	branch := strings.TrimSpace(string(out))
	// Check for uncommitted changes
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = c.sess.CWD
	statusOut, _ := statusCmd.Output()
	if len(statusOut) > 0 {
		branch += "*"
	}
	c.mu.Lock()
	c.gitBranch = branch
	c.mu.Unlock()
}

func NewCLI(sm *session.Manager, reg *registry.Registry, eng *engine.Engine, defaultClient string) *CLI {
	return &CLI{
		Sm:            sm,
		Reg:           reg,
		Engine:        eng,
		DefaultClient: defaultClient,
		In:            os.Stdin,
		Out:           os.Stdout,
	}
}

const (
	escSaveCursor    = "\x1b[s"
	escRestoreCursor = "\x1b[u"
	escClearLine     = "\x1b[2K"
	EscClear         = "\x1b[2J\x1b[H"
	escReset         = "\x1b[0m"
	escBlueWhite     = "\x1b[44;37m"
	escCyan          = "\x1b[36m"
	escBoldCyan      = "\x1b[1;36m"
	escGreen         = "\x1b[32m"
	escGray          = "\x1b[90m"
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
	DrawerHeight      = 8
	DoubleTabInterval = 300 * time.Millisecond
	PromptNormal      = "› "
	PromptPulse       = "› "
	PromptOffset      = 2
	Margin            = "  "
	MarginWidth       = 2
)

func (c *CLI) getPrompt() string {
	if !c.isThinking {
		return PromptNormal
	}
	if c.pulseFrame%2 == 1 {
		return PromptPulse
	}
	return PromptNormal
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

const AsciiBanner = `
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

// writeInScrollRegion writes content into the scroll region.
// During streaming the cursor is already there, so a plain write suffices.
// Otherwise it saves the cursor, jumps to the scroll-region bottom,
// writes, and restores.
func (c *CLI) writeInScrollRegion(content string) {
	c.mu.Lock()
	inRaw := c.inRawMode
	streaming := c.isStreaming
	c.mu.Unlock()
	if !inRaw {
		c.write(content)
		return
	}
	if streaming {
		c.write(content)
		return
	}
	rows, _ := c.getTermSize()
	scrollBottom := rows - 5
	if c.IsImmersive {
		scrollBottom = rows - DrawerHeight - 6
	}
	if scrollBottom < 1 {
		scrollBottom = 1
	}
	c.write(escSaveCursor + fmt.Sprintf(escMoveTo, scrollBottom) + content + escRestoreCursor)
}

func (c *CLI) drawBrandingAtomic(sb *strings.Builder) {
	lines := strings.Split(strings.Trim(AsciiBanner, "\n"), "\n")
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
		fmt.Fprintf(sb, "%s%s%s%s\n", Margin, color, line, escReset)
	}
	fmt.Fprintf(sb, "\n%s%s TENAZAS — This is the (Gate)way %s\n\n", Margin, escBoldCyan, escReset)

	if c.sess != nil {
		fmt.Fprintf(sb, "%s%sSession: %s%s\n", Margin, escDim, c.sess.ID, escReset)
		fmt.Fprintf(sb, "%s%sPath:    %s%s\n", Margin, escDim, c.sess.CWD, escReset)
		clientName := c.sess.Client
		if clientName == "" {
			clientName = c.DefaultClient
		}
		fmt.Fprintf(sb, "%s%sClient:  %s%s\n\n", Margin, escDim, clientName, escReset)
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

	c.writeEscape(EscClear)
	c.setupTerminal()
	c.refreshSkillCount()
	c.refreshGitBranch()
	defer c.writeEscape("\x1b[r")

	c.drawBranding()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGWINCH)
	go func() {
		for range sigChan {
			var sb strings.Builder
			c.writeAtomic(&sb, func(sb *strings.Builder) {
				sb.WriteString(escHideCursor)
				sb.WriteString(escSaveCursor)
				c.setupTerminalAtomic(sb)
				c.drawDrawerAtomic(sb)
				sb.WriteString(escRestoreCursor)
				c.renderLineAtomic(sb)
				sb.WriteString(escShowCursor)
			})
		}
	}()

	c.write(Margin + "Commands: /run <skill>, /last <N>, /intervene <action>, /mode, /budget, /help\n")

	if resume {
		c.replayHistory(sess)
		if sess.SkillName != "" {
			c.resumeSkill(sess)
		}
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

func (c *CLI) selectSession() (*models.Session, error) {
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
			c.write("No sessions found to resume.\n")
			return nil, fmt.Errorf("no sessions to resume")
		}

		var sb strings.Builder
		sb.WriteString(EscClear)
		sb.WriteString(escHideCursor)
		defer c.write(escShowCursor)

		totalPages := (total + pageSize - 1) / pageSize
		fmt.Fprintf(&sb, "Select a session to resume (Page %d/%d):\n\n", page+1, totalPages)

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
			sk := ""
			if s.SkillName != "" {
				sk = fmt.Sprintf("(%s)", s.SkillName)
			}
			fmt.Fprintf(&sb, "%s%-60s %s [%s] %s\n", cursor, title, ts, s.ID[:8], sk)
		}
		fmt.Fprintf(&sb, "\n  %sUse ↑/↓ to navigate, ←/→ for pages, Enter to select, q to quit.%s\n", escDim, escReset)
		c.write(sb.String())

		reader := bufio.NewReader(c.In)
		r, _, err := reader.ReadRune()
		if err != nil {
			return nil, err
		}

		switch r {
		case 'q', '\x03':
			return nil, fmt.Errorf("aborted")
		case '\r', '\n':
			if selectedIndex < len(sessions) {
				return &sessions[selectedIndex], nil
			}
		case '\x1b':
			if reader.Buffered() > 0 {
				r2, _, _ := reader.ReadRune()
				if r2 == '[' {
					r3, _, _ := reader.ReadRune()
					switch r3 {
					case 'A':
						if selectedIndex > 0 {
							selectedIndex--
						}
					case 'B':
						if selectedIndex < len(sessions)-1 {
							selectedIndex++
						}
					case 'C':
						if (page+1)*pageSize < total {
							page++
							selectedIndex = 0
						}
					case 'D':
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

func (c *CLI) initializeSession(resume bool) (*models.Session, error) {
	if resume {
		return c.selectSession()
	}

	cwd, _ := os.Getwd()
	sess := &models.Session{
		ID:           uuid.New().String(),
		Client:       c.DefaultClient,
		CWD:          cwd,
		LastUpdated:  time.Now(),
		RoleCache:    make(map[string]string),
		ApprovalMode: models.ApprovalModePlan,
	}
	c.Sm.Save(sess)
	return sess, nil
}

func (c *CLI) resumeSkill(sess *models.Session) {
	sk, err := c.Sm.LoadSkill(sess.SkillName)
	if err == nil {
		if sess.Status != models.StatusRunning && sess.Status != models.StatusIntervention {
			c.write(fmt.Sprintf("Resuming task: %s (Skill: %s)\n", sess.ID, sess.SkillName))
			sess.Status = models.StatusRunning
			c.Sm.Save(sess)
		}
		go c.Engine.Run(sk, sess)
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
	eventCh := events.GlobalBus.Subscribe()
	f := &formatter.AnsiFormatter{}

	for e := range eventCh {
		if e.SessionID == sessionID && e.Type == events.EventAudit {
			audit, ok := e.Payload.(events.AuditEntry)
			if !ok {
				continue
			}

			if audit.Type == events.AuditLLMPrompt {
				c.mu.Lock()
				c.lastThought = ""
				c.isStreaming = false
				c.mu.Unlock()
				c.setThinking(true)
			} else if audit.Type == events.AuditLLMChunk || audit.Type == events.AuditLLMThought {
				c.mu.Lock()
				wasStreaming := c.isStreaming
				c.isStreaming = true
				c.mu.Unlock()
				if !wasStreaming {
					// Move cursor into scroll region before first chunk
					rows, _ := c.getTermSize()
					scrollEnd := rows - 5
					if c.IsImmersive {
						scrollEnd = rows - DrawerHeight - 6
					}
					if scrollEnd < 1 {
						scrollEnd = 1
					}
					c.write(fmt.Sprintf(escMoveTo, scrollEnd))
				}
				c.setThinking(false)
			}

			if audit.Type == events.AuditLLMThought {
				c.mu.Lock()
				c.lastThought += audit.Content
				c.mu.Unlock()
				c.addThought(audit.Content)
				continue
			}

			if audit.Type == events.AuditLLMChunk {
				c.writeInScrollRegion(audit.Content)
				continue
			}

			if audit.Type == events.AuditLLMResponse {
				c.mu.Lock()
				c.isStreaming = false
				c.mu.Unlock()
				continue
			}

			if audit.Type == events.AuditCmdResult || audit.Type == events.AuditStatus || audit.Type == events.AuditInfo {
				c.mu.Lock()
				c.isStreaming = false
				c.mu.Unlock()
				formatted := f.Format(audit)
				c.addThought(formatted)
				c.mu.Lock()
				isImm := c.IsImmersive
				c.mu.Unlock()
				if isImm {
					continue
				}
			}

			c.writeInScrollRegion(fmt.Sprintf("\n%s%s\n", Margin, f.Format(audit)))
			if audit.Type == events.AuditIntervention {
				c.writeInScrollRegion(fmt.Sprintf("\n%sType `/intervene <retry|proceed_to_fail|abort>`\n", Margin))
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

	commands := []string{"/run", "/last", "/intervene", "/skills", "/mode", "/budget", "/help"}

	if strings.HasPrefix(line, "/run ") {
		prefix := strings.TrimPrefix(line, "/run ")
		skills, err := skill.List(c.Sm.StoragePath)
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
	sb.WriteString(EscClear)
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

func (c *CLI) handleTabLocked(sess *models.Session) {
	now := time.Now()
	if !c.lastTabTime.IsZero() && now.Sub(c.lastTabTime) < DoubleTabInterval {
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
	// While streaming, save/restore so we don't displace the scroll-region cursor
	if c.isStreaming {
		sb.WriteString(escSaveCursor)
		defer sb.WriteString(escRestoreCursor)
	}

	rows, cols := c.getTermSize()
	promptRow := c.promptRow(rows)

	prompt := Margin + c.getPrompt()
	prefixLen := MarginWidth + PromptOffset

	line := string(c.input)

	suggestion := ""
	if !c.isThinking {
		suggestion = c.getDimmedSuggestion(line)
	}

	// Calculate available width for text per line
	availWidth := cols - prefixLen
	if availWidth < 10 {
		availWidth = 10
	}

	// Wrap the input text
	wrappedLines := wrapText(line, availWidth)
	currentLines := len(wrappedLines)
	if currentLines < 1 {
		currentLines = 1
	}

	continuationIndent := strings.Repeat(" ", prefixLen)

	// Prompt grows upward: last line stays at promptRow (rows-2)
	startRow := promptRow - (currentLines - 1)

	// Clear leftover rows above from a previous longer render
	prevStartRow := promptRow - (c.lastRenderLines - 1)
	if c.lastRenderLines > currentLines && prevStartRow < startRow {
		for r := prevStartRow; r < startRow; r++ {
			fmt.Fprintf(sb, escMoveTo, r)
			sb.WriteString(escClearLine)
		}
	}

	if c.isThinking {
		sb.WriteString(escCyan)
	}

	// Write first line with prompt at startRow
	fmt.Fprintf(sb, escMoveTo, startRow)
	sb.WriteString(escClearLine)
	if len(wrappedLines) == 0 {
		sb.WriteString(prompt)
	} else {
		sb.WriteString(prompt)
		sb.WriteString(wrappedLines[0])
	}

	if c.isThinking {
		sb.WriteString(escReset)
	}

	// Show suggestion only on single-line input
	if suggestion != "" && len(wrappedLines) <= 1 {
		fmt.Fprintf(sb, "%s%s%s", escDim, suggestion, escReset)
	}

	sb.WriteString(escClearToEOL)

	// Write continuation lines downward from startRow
	for i := 1; i < len(wrappedLines); i++ {
		fmt.Fprintf(sb, escMoveTo, startRow+i)
		sb.WriteString(escClearLine)
		sb.WriteString(continuationIndent)
		sb.WriteString(wrappedLines[i])
	}

	// Track how many lines this render used
	c.lastRenderLines = currentLines
	c.promptLines = currentLines

	// Move cursor to the correct position within the wrapped text
	cursorLine, cursorCol := c.cursorPosition(wrappedLines, availWidth)
	fmt.Fprintf(sb, escMoveTo, startRow+cursorLine)
	fmt.Fprintf(sb, escCR+escMoveRight, cursorCol+prefixLen)
}

func wrapText(text string, width int) []string {
	if width <= 0 || len(text) == 0 {
		return []string{text}
	}
	runes := []rune(text)
	var lines []string
	for len(runes) > width {
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	lines = append(lines, string(runes))
	return lines
}

func (c *CLI) cursorPosition(wrappedLines []string, availWidth int) (line, col int) {
	if availWidth <= 0 || c.cursorPos == 0 {
		return 0, c.cursorPos
	}
	line = c.cursorPos / availWidth
	col = c.cursorPos % availWidth
	if line >= len(wrappedLines) {
		line = len(wrappedLines) - 1
		col = len([]rune(wrappedLines[line]))
	}
	return line, col
}

func (c *CLI) renderLine() {
	var sb strings.Builder
	c.writeAtomic(&sb, func(sb *strings.Builder) {
		c.renderLineAtomic(sb)
	})
}

func (c *CLI) handleCommand(sess *models.Session, text string) {
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
	case "/budget":
		c.handleBudget(sess, parts[1:])
	case "/help":
		c.handleHelp()
	default:
		go c.Engine.ExecutePrompt(sess, text)
	}
}

func (c *CLI) repl(sess *models.Session) error {
	fd := int(syscall.Stdin)
	if oldState, err := enableRawMode(fd); err == nil {
		c.inRawMode = true
		c.oldTermState = oldState
		defer restoreTerminal(fd, oldState)
		return c.replRaw(sess)
	}

	scanner := bufio.NewScanner(c.In)
	for {
		c.write("\n" + Margin + PromptNormal)
		if !scanner.Scan() {
			return io.EOF
		}
		text := scanner.Text()
		if text != "" {
			c.handleCommand(sess, text)
		}
	}
}

func (c *CLI) replRaw(sess *models.Session) error {
	c.inRawMode = true
	reader := bufio.NewReader(c.In)
	for {
		c.renderLine()
		r, _, err := reader.ReadRune()
		if err != nil {
			return err
		}

		c.mu.Lock()
		if r != '\t' {
			c.lastTabTime = time.Time{}
		}

		switch r {
		case '\x03':
			c.mu.Unlock()
			return nil
		case '\r', '\n':
			line := string(c.input)
			c.input = nil
			c.cursorPos = 0
			c.lastRenderLines = 0
			c.promptLines = 0
			c.resetCompletionsLocked()
			c.mu.Unlock()

			// Move cursor to end of scrolling region so output flows there
			rows, _ := c.getTermSize()
			scrollEnd := rows - 5
			if c.IsImmersive {
				scrollEnd = rows - DrawerHeight - 6
			}
			if scrollEnd < 1 {
				scrollEnd = 1
			}
			c.write(fmt.Sprintf(escMoveTo, scrollEnd) + "\n")
			if line != "" {
				// Echo user prompt in scroll region and save content cursor
				c.write(Margin + escBoldCyan + "› " + escReset + line + "\n")
				c.handleCommand(sess, line)
			}
		case '\t':
			c.handleTabLocked(sess)
			c.mu.Unlock()
		case '\x7f', '\x08':
			c.handleBackspaceLocked()
			c.mu.Unlock()
		case '\x1b':
			c.mu.Unlock()
			// Wait briefly to distinguish bare Escape from escape sequences (arrow keys, shift+tab)
			time.Sleep(50 * time.Millisecond)
			if reader.Buffered() > 0 {
				c.handleEscape(reader, sess)
			} else {
				c.handleBareEscape(sess)
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

	all, _ := skill.List(c.Sm.StoragePath)
	active, _ := c.Sm.GetActiveSkills()

	activeMap := make(map[string]bool)
	for _, s := range active {
		activeMap[s] = true
	}

	c.write("STATUS  NAME\n")
	for _, s := range all {
		status := "[ ]"
		if activeMap[s] {
			status = "[X]"
		}
		c.write(fmt.Sprintf("%-7s %s\n", status, s))
	}
}

func (c *CLI) handleRun(sess *models.Session, skillName string) {
	sk, err := c.Sm.LoadSkill(skillName)
	if err != nil {
		c.write(fmt.Sprintln("Skill error:", err))
		return
	}
	sess.SkillName = skillName
	c.Sm.Save(sess)
	go c.Engine.Run(sk, sess)
}

func (c *CLI) handleLast(sess *models.Session, n int) {
	logs, _ := c.Sm.GetLastAudit(sess, n)
	f := &formatter.AnsiFormatter{}
	var output strings.Builder
	for _, l := range logs {
		fmt.Fprintln(&output, f.Format(l))
	}
	c.write(output.String())
}

const replayHistoryEntries = 50

func (c *CLI) replayHistory(sess *models.Session) {
	logs, err := c.Sm.GetLastAudit(sess, replayHistoryEntries)
	if err != nil || len(logs) == 0 {
		return
	}
	f := &formatter.AnsiFormatter{}
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%s%s── Session History ──%s\n\n", Margin, escDim, escReset)
	for _, entry := range logs {
		switch entry.Type {
		case events.AuditLLMPrompt:
			fmt.Fprintf(&sb, "%s%s› %s%s\n", Margin, escBoldCyan, entry.Content, escReset)
		case events.AuditLLMResponse:
			fmt.Fprintf(&sb, "%s\n", entry.Content)
		case events.AuditLLMChunk, events.AuditLLMThought:
			continue
		default:
			fmt.Fprintf(&sb, "%s%s\n", Margin, f.Format(entry))
		}
	}
	fmt.Fprintf(&sb, "\n%s%s── End of History ──%s\n\n", Margin, escDim, escReset)
	c.write(sb.String())
}

// FooterData holds all values rendered in the status bar.
type FooterData struct {
	Mode         string
	Yolo         bool
	ModelTier    string
	MaxBudgetUSD float64
	SkillCount   int
	CWD          string
	Hint         string
	GitBranch    string
	ClientName   string
}

// FormatFooterLine1 returns the first footer line: path [branch] on left, client (tier) on right.
func FormatFooterLine1(d FooterData, cols int) string {
	dir := d.CWD
	if home, err := os.UserHomeDir(); err == nil {
		dir = strings.Replace(dir, home, "~", 1)
	}

	left := dir
	if d.GitBranch != "" {
		left += " [" + d.GitBranch + "]"
	}

	var rightParts []string
	if d.ClientName != "" {
		part := d.ClientName
		if d.ModelTier != "" {
			part += " (" + d.ModelTier + ")"
		}
		rightParts = append(rightParts, part)
	}
	if d.MaxBudgetUSD > 0 {
		rightParts = append(rightParts, fmt.Sprintf("$%.2f", d.MaxBudgetUSD))
	}
	right := strings.Join(rightParts, " · ")

	gap := cols - len(left) - len(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

// ModeColor returns the ANSI color escape for the current approval mode.
func ModeColor(mode string, yolo bool) string {
	if yolo {
		return escGreen
	}
	switch mode {
	case models.ApprovalModePlan:
		return escCyan
	default:
		return escGray
	}
}

// FormatFooterLine2 returns the second footer line: keybinding hints left, skills right.
func FormatFooterLine2(d FooterData, cols int) string {
	displayMode := d.Mode
	if d.Yolo {
		displayMode = models.ApprovalModeYolo
	} else if displayMode == "" {
		displayMode = models.ApprovalModePlan
	}

	left := "shift+tab " + displayMode + " · ctrl+s run skill"
	right := fmt.Sprintf("Skills: %d", d.SkillCount)

	gap := cols - len(left) - len(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (c *CLI) handleMode(sess *models.Session, args []string) {
	if len(args) == 0 {
		c.write(fmt.Sprintf("Current mode: %s (Yolo: %v)\n", sess.ApprovalMode, sess.Yolo))
		return
	}
	c.setApprovalMode(sess, args[0])
}

func (c *CLI) handleBudget(sess *models.Session, args []string) {
	if len(args) == 0 {
		if sess.MaxBudgetUSD <= 0 {
			c.write("Budget: unlimited\n")
		} else {
			c.write(fmt.Sprintf("Budget: $%.2f\n", sess.MaxBudgetUSD))
		}
		return
	}
	var amount float64
	if _, err := fmt.Sscanf(args[0], "%f", &amount); err != nil || amount < 0 {
		c.write("Invalid budget. Use: /budget <amount> (e.g. /budget 5.00, /budget 0 for unlimited)\n")
		return
	}
	c.mu.Lock()
	sess.MaxBudgetUSD = amount
	c.persistSession(sess)
	c.drawFooterLocked(sess)
	c.mu.Unlock()
	if amount <= 0 {
		c.write("Budget set to unlimited.\n")
	} else {
		c.write(fmt.Sprintf("Budget set to $%.2f.\n", amount))
	}
}

func (c *CLI) persistSession(sess *models.Session) {
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
	fmt.Fprintln(&output, "  /budget <amount>     Set session budget cap (0 = unlimited)")
	fmt.Fprintln(&output, "  /help                Show this help")
	fmt.Fprintln(&output, "\nModes: plan, auto_edit, yolo")
	c.write(output.String())
}

func (c *CLI) drawFooterAtomic(sb *strings.Builder, sess *models.Session) {
	if sess == nil {
		return
	}
	rows, cols := c.getTermSize()

	clientName := sess.Client
	if clientName == "" {
		clientName = c.DefaultClient
	}

	d := FooterData{
		Mode:         sess.ApprovalMode,
		Yolo:         sess.Yolo,
		ModelTier:    sess.ModelTier,
		MaxBudgetUSD: sess.MaxBudgetUSD,
		SkillCount:   c.skillCount,
		CWD:          sess.CWD,
		Hint:         c.lastThought,
		GitBranch:    c.gitBranch,
		ClientName:   clientName,
	}

	line1 := FormatFooterLine1(d, cols)
	line2 := FormatFooterLine2(d, cols)

	sb.WriteString(escSaveCursor)
	color := ModeColor(sess.ApprovalMode, sess.Yolo)
	// Extra lines from multi-line prompt (grows upward)
	extra := c.promptLines - 1
	if extra < 0 {
		extra = 0
	}
	// Footer line 1 (path/branch/client) shifts up with prompt
	fmt.Fprintf(sb, escMoveTo, rows-4-extra)
	sb.WriteString(escClearLine)
	sb.WriteString(escBlueWhite)
	fmt.Fprintf(sb, "%-*s", cols, line1)
	sb.WriteString(escReset)
	// Top separator shifts up with prompt
	fmt.Fprintf(sb, escMoveTo, rows-3-extra)
	sb.WriteString(escClearLine)
	sb.WriteString(color)
	sb.WriteString(strings.Repeat("─", cols))
	sb.WriteString(escReset)
	// Prompt area (rows-2-extra to rows-2) is handled by renderLineAtomic
	// Bottom separator at rows-1 (fixed)
	fmt.Fprintf(sb, escMoveTo, rows-1)
	sb.WriteString(escClearLine)
	sb.WriteString(color)
	sb.WriteString(strings.Repeat("─", cols))
	sb.WriteString(escReset)
	// Footer line 2 (keybindings/skills) at rows (fixed)
	fmt.Fprintf(sb, escMoveTo, rows)
	sb.WriteString(escClearLine)
	sb.WriteString(color)
	fmt.Fprintf(sb, "%-*s", cols, line2)
	sb.WriteString(escReset)
	sb.WriteString(escRestoreCursor)
}

func (c *CLI) handleEscape(reader *bufio.Reader, sess *models.Session) {
	r2, _, _ := reader.ReadRune()
	if r2 != '[' {
		return
	}
	r3, _, _ := reader.ReadRune()
	c.mu.Lock()
	defer c.mu.Unlock()
	switch r3 {
	case 'C':
		if c.cursorPos < len(c.input) {
			c.cursorPos++
		}
	case 'D':
		if c.cursorPos > 0 {
			c.cursorPos--
		}
	case 'Z':
		c.cycleModeLocked(sess)
	}
}

func (c *CLI) handleBareEscape(sess *models.Session) {
	c.mu.Lock()
	isRunning := c.isThinking
	hasInput := len(c.input) > 0
	now := time.Now()
	isDoubleEsc := !c.lastEscTime.IsZero() && now.Sub(c.lastEscTime) < DoubleTabInterval
	c.lastEscTime = now
	c.mu.Unlock()

	if isRunning {
		// Single escape while LLM is working → cancel
		c.Engine.CancelSession(sess.ID)
		c.setThinking(false)
		c.write(fmt.Sprintf("\n%s%s● Operation cancelled%s\n", Margin, "\x1b[33m", escReset))
		return
	}

	if hasInput && isDoubleEsc {
		// Double escape while typing → clear input
		c.mu.Lock()
		c.input = nil
		c.cursorPos = 0
		c.lastRenderLines = 0
		c.resetCompletionsLocked()
		c.lastEscTime = time.Time{}
		c.mu.Unlock()
		c.renderLine()
	}
}

func (c *CLI) cycleMode(sess *models.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cycleModeLocked(sess)
}

func (c *CLI) setApprovalMode(sess *models.Session, mode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setApprovalModeLocked(sess, mode)
}

func (c *CLI) setApprovalModeLocked(sess *models.Session, mode string) {
	mode = strings.ToUpper(mode)
	switch mode {
	case models.ApprovalModeYolo:
		sess.Yolo = true
		sess.ApprovalMode = models.ApprovalModeYolo
	case models.ApprovalModePlan, models.ApprovalModeAutoEdit:
		sess.Yolo = false
		sess.ApprovalMode = mode
	default:
		c.writeLocked(fmt.Sprintf("Invalid mode: %s. Use plan, auto_edit, or yolo.\n", mode))
		return
	}
	c.persistSession(sess)
	c.drawFooterLocked(sess)
}

func (c *CLI) cycleModeLocked(sess *models.Session) {
	newMode := models.ApprovalModePlan
	if sess.Yolo {
		newMode = models.ApprovalModePlan
	} else {
		switch sess.ApprovalMode {
		case models.ApprovalModePlan:
			newMode = models.ApprovalModeAutoEdit
		case models.ApprovalModeAutoEdit:
			newMode = models.ApprovalModeYolo
		default:
			newMode = models.ApprovalModePlan
		}
	}
	c.setApprovalModeLocked(sess, newMode)
}

func (c *CLI) drawFooter(sess *models.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drawFooterLocked(sess)
}

func (c *CLI) drawFooterLocked(sess *models.Session) {
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
	return rows - DrawerHeight - 4
}

func (c *CLI) promptRow(rows int) int {
	return rows - 2
}

func (c *CLI) setupTerminalAtomic(sb *strings.Builder) {
	rows, _ := c.getTermSize()

	// Clear old reserved rows to avoid ghost footers/drawers on resize.
	if c.lastRows > 0 && c.lastRows != rows {
		oldBottom := 5
		if c.IsImmersive {
			oldBottom = DrawerHeight + 6
		}
		for r := c.lastRows - oldBottom; r <= c.lastRows; r++ {
			if r > 0 {
				fmt.Fprintf(sb, escMoveTo, r)
				sb.WriteString(escClearLine)
			}
		}
	}
	c.lastRows = rows

	bottomReserved := 5
	if c.IsImmersive {
		bottomReserved = DrawerHeight + 6
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
		lines := strings.Split(text, "\n")
		for _, l := range lines {
			if l == "" {
				continue
			}
			c.drawer = append(c.drawer, l)
		}
		if len(c.drawer) > DrawerHeight {
			c.drawer = c.drawer[len(c.drawer)-DrawerHeight:]
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
	for i := 0; i < DrawerHeight; i++ {
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
