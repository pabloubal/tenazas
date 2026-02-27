package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tenazas/internal/events"
	"tenazas/internal/formatter"
	"tenazas/internal/models"
	"tenazas/internal/registry"
	"tenazas/internal/session"
)

type Telegram struct {
	Token          string
	AllowedIDs     []int64
	UpdateInterval int
	Sm             *session.Manager
	Reg            *registry.Registry
	Engine         models.EngineInterface
	DefaultClient  string
	lastUpdateID   int64
	activeMessages map[string]*tgLiveStream
	mu             sync.RWMutex
}

// SendNotification implements heartbeat.Notifier.
func (tg *Telegram) SendNotification(chatID int64, text string) {
	tg.send(chatID, text)
}

// AllowedChatIDs implements heartbeat.Notifier.
func (tg *Telegram) AllowedChatIDs() []int64 {
	return tg.AllowedIDs
}

type tgLiveStream struct {
	msgID    int64
	fullText string
	lastEdit time.Time
}

func (tg *Telegram) streamKey(chatID int64, sessionID string) string {
	return fmt.Sprintf("%d:%s", chatID, sessionID)
}

// Telegram API Structs
type TgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  struct {
		MessageID int64 `json:"message_id"`
		From      struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Text string `json:"text"`
	} `json:"message"`
	CallbackQuery struct {
		ID   string `json:"id"`
		From struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Data string `json:"data"`
	} `json:"callback_query"`
}

type TgResponse struct {
	OK     bool       `json:"ok"`
	Result []TgUpdate `json:"result"`
}

type TgMessageResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code,omitempty"`
	Description string `json:"description,omitempty"`
	Result      struct {
		MessageID int64 `json:"message_id"`
	} `json:"result"`
}

func (tg *Telegram) IsAllowed(id int64) bool {
	for _, aid := range tg.AllowedIDs {
		if aid == id {
			return true
		}
	}
	return false
}

var BaseURL = "https://api.telegram.org/bot"

func (tg *Telegram) Call(method string, payload interface{}) ([]byte, error) {
	url := fmt.Sprintf("%s%s/%s", BaseURL, tg.Token, method)
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal error: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (tg *Telegram) send(chatID int64, text string, options ...map[string]interface{}) {
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	for _, opt := range options {
		for k, v := range opt {
			payload[k] = v
		}
	}
	if _, err := tg.Call("sendMessage", payload); err != nil {
		fmt.Printf("Telegram error in send: %v\n", err)
	}
}

type tgJob struct {
	chatID int64
	text   string
	data   string
	cb     bool
}

func (tg *Telegram) dispatch(f func()) {
	if tg.UpdateInterval > 0 {
		go f()
	} else {
		f()
	}
}

func (tg *Telegram) Poll() {
	if tg.UpdateInterval < 1000 {
		tg.UpdateInterval = 1000
	}

	jobs := make(chan tgJob, 100)
	for i := 0; i < 5; i++ {
		go func() {
			for j := range jobs {
				if j.cb {
					tg.HandleCallback(j.chatID, j.data)
				} else {
					tg.HandleMessage(j.chatID, j.text)
				}
			}
		}()
	}

	eventCh := events.GlobalBus.Subscribe()
	go tg.listenEvents(eventCh)

	for {
		data, err := tg.Call("getUpdates", map[string]interface{}{
			"offset":  tg.lastUpdateID + 1,
			"timeout": 30,
		})
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

		var res TgResponse
		if err := json.Unmarshal(data, &res); err != nil {
			continue
		}

		for _, upd := range res.Result {
			tg.lastUpdateID = upd.UpdateID
			fromID := upd.Message.From.ID
			if fromID == 0 {
				fromID = upd.CallbackQuery.From.ID
			}

			if !tg.IsAllowed(fromID) {
				continue
			}

			if upd.Message.Text != "" {
				jobs <- tgJob{chatID: fromID, text: upd.Message.Text}
			} else if upd.CallbackQuery.Data != "" {
				jobs <- tgJob{chatID: fromID, data: upd.CallbackQuery.Data, cb: true}
			}
		}
	}
}

func (tg *Telegram) listenEvents(ch chan events.Event) {
	f := &formatter.HtmlFormatter{}
	for e := range ch {
		switch e.Type {
		case events.EventAudit:
			tg.broadcastAudit(e.SessionID, e.Payload.(events.AuditEntry), f)
		case events.EventTaskStatus:
			payload := e.Payload.(events.TaskStatusPayload)
			tg.NotifyTaskState(e.SessionID, payload.State, payload.Details)
		}
	}
}

func (tg *Telegram) broadcastAudit(sessionID string, audit events.AuditEntry, f *formatter.HtmlFormatter) {
	tg.mu.Lock()
	if tg.activeMessages == nil {
		tg.activeMessages = make(map[string]*tgLiveStream)
	}
	tg.mu.Unlock()

	for _, id := range tg.AllowedIDs {
		instanceID := tg.instanceID(id)
		state, _ := tg.Reg.Get(instanceID)

		switch audit.Type {
		case events.AuditLLMChunk:
			tg.handleStreamingChunk(id, sessionID, audit.Content, f)
			continue
		case events.AuditLLMResponse:
			tg.handleStreamingEnd(id, sessionID, audit.Content, f)
			continue
		}

		if state.SessionID != sessionID {
			continue
		}

		if tg.shouldDisplay(state.Verbosity, audit.Type) {
			tg.sendAuditMessage(id, sessionID, audit, f)
		}
	}
}

func (tg *Telegram) shouldDisplay(verbosity, auditType string) bool {
	if auditType == events.AuditLLMThought {
		return false
	}
	switch verbosity {
	case "LOW":
		return auditType == events.AuditIntervention || auditType == events.AuditStatus
	case "MEDIUM":
		return auditType == events.AuditIntervention || auditType == events.AuditInfo || auditType == events.AuditStatus
	case "HIGH":
		return true
	}
	return false
}

func (tg *Telegram) sendAuditMessage(chatID int64, sessionID string, audit events.AuditEntry, f *formatter.HtmlFormatter) {
	prefix, suffix := "", ""
	if sess, _ := tg.Sm.Load(sessionID); sess != nil && sess.Yolo {
		prefix, suffix = "⚠️ <b>YOLO MODE</b> ⚠️\n\n", "\n\n⚠️ <b>YOLO MODE</b> ⚠️"
	}

	content := f.Format(audit)
	if audit.Type == events.AuditIntervention {
		tg.sendIntervention(chatID, sessionID, prefix+content+suffix)
	} else {
		tg.send(chatID, prefix+content+suffix)
	}
}

func (tg *Telegram) handleStreamingChunk(id int64, sessionID, content string, f *formatter.HtmlFormatter) {
	key := tg.streamKey(id, sessionID)
	tg.mu.RLock()
	stream, ok := tg.activeMessages[key]
	tg.mu.RUnlock()

	if !ok {
		tg.mu.Lock()
		stream, ok = tg.activeMessages[key]
		if !ok {
			msgData, err := tg.Call("sendMessage", map[string]interface{}{
				"chat_id":    id,
				"text":       "<i>...</i>",
				"parse_mode": "HTML",
			})
			if err == nil {
				var msgRes TgMessageResponse
				if json.Unmarshal(msgData, &msgRes) == nil && msgRes.OK {
					stream = &tgLiveStream{msgID: msgRes.Result.MessageID, lastEdit: time.Now()}
					tg.activeMessages[key] = stream
				}
			}
		}
		tg.mu.Unlock()
		if stream == nil {
			return
		}
	}

	tg.mu.Lock()
	defer tg.mu.Unlock()

	stream.fullText += content
	interval := time.Duration(tg.UpdateInterval) * time.Millisecond
	if interval < 1000*time.Millisecond {
		interval = 1000 * time.Millisecond
	}

	if time.Since(stream.lastEdit) > interval {
		text := f.Escape(stream.fullText)
		if len(text) > 4000 {
			text = text[:3997] + "..."
		}
		_, _ = tg.Call("editMessageText", map[string]interface{}{
			"chat_id":    id,
			"message_id": stream.msgID,
			"text":       text,
			"parse_mode": "HTML",
		})
		stream.lastEdit = time.Now()
	}
}

func (tg *Telegram) handleStreamingEnd(id int64, sessionID, content string, f *formatter.HtmlFormatter) {
	markup := getActionKeyboard(sessionID, content)
	text := "🟢 <b>RESPONSE:</b>\n" + f.Escape(content)

	key := tg.streamKey(id, sessionID)
	tg.mu.Lock()
	stream, ok := tg.activeMessages[key]
	var msgID int64
	if ok {
		msgID = stream.msgID
		delete(tg.activeMessages, key)
	}
	tg.mu.Unlock()

	_, _ = tg.upsertMonitoringMessage(id, msgID, text, markup)
}

func (tg *Telegram) sendIntervention(id int64, sessionID string, text string) {
	tg.send(id, text, map[string]interface{}{
		"reply_markup": map[string]interface{}{
			"inline_keyboard": [][]map[string]interface{}{
				{tgBtn("🔄 Retry", "intv:retry:"+sessionID), tgBtn("⏩ Proceed", "intv:proceed_to_fail:"+sessionID)},
				{tgBtn("🛑 Abort", "intv:abort:"+sessionID)},
			},
		},
	})
}

func (tg *Telegram) NotifyTaskState(sessionID string, state string, details map[string]string) {
	sess, err := tg.Sm.Load(sessionID)
	if err != nil {
		return
	}

	chatID := sess.MonitoringChatID
	if chatID == 0 && len(tg.AllowedIDs) > 0 {
		chatID = tg.AllowedIDs[0]
	}

	if chatID == 0 {
		return
	}

	text := tg.formatTaskStatusText(sess, state, details)
	keyboard := tg.getTaskStatusKeyboard(sessionID, state)

	msgID, err := tg.upsertMonitoringMessage(chatID, sess.MonitoringMessageID, text, keyboard)
	if err == nil && msgID != sess.MonitoringMessageID {
		sess.MonitoringMessageID = msgID
		sess.MonitoringChatID = chatID
		if err := tg.Sm.Save(sess); err != nil {
			fmt.Printf("Error saving session after task status update: %v\n", err)
		}
	}
}

func (tg *Telegram) upsertMonitoringMessage(chatID, msgID int64, text string, keyboard map[string]interface{}) (int64, error) {
	if len(text) > 4000 {
		text = text[:3997] + "..."
	}

	payload := map[string]interface{}{
		"chat_id":      chatID,
		"text":         text,
		"parse_mode":   "HTML",
		"reply_markup": keyboard,
	}

	if msgID != 0 {
		payload["message_id"] = msgID
		resp, err := tg.Call("editMessageText", payload)
		if err == nil {
			var tgRes TgMessageResponse
			if err := json.Unmarshal(resp, &tgRes); err == nil {
				if tgRes.OK || strings.Contains(tgRes.Description, "message is not modified") {
					return msgID, nil
				}
			}
		}
		delete(payload, "message_id")
	}

	resp, err := tg.Call("sendMessage", payload)
	if err != nil {
		return 0, fmt.Errorf("failed to send message: %v", err)
	}

	var tgRes TgMessageResponse
	if err := json.Unmarshal(resp, &tgRes); err == nil && tgRes.OK {
		return tgRes.Result.MessageID, nil
	}

	return 0, fmt.Errorf("failed to parse telegram response")
}

var stateMeta = map[string]struct {
	icon, label, btn, act string
}{
	events.TaskStateStarted:   {"🚀", "STARTED", "⏸️ Pause", "task_pause"},
	events.TaskStateBlocked:   {"⏸️", "BLOCKED", "💬 Respond & Unblock", "task_respond"},
	events.TaskStateCompleted: {"✅", "COMPLETED", "🔍 Review Output", "task_review"},
	events.TaskStateFailed:    {"❌", "FAILED", "🔍 Review Output", "task_review"},
}

func (tg *Telegram) formatTaskStatusText(sess *models.Session, state string, details map[string]string) string {
	meta := stateMeta[state]
	icon, label := meta.icon, meta.label
	if icon == "" {
		icon, label = "⏳", state
	}

	title := sess.Title
	if title == "" {
		title = sess.SkillName
	}
	if title == "" {
		title = sess.ID
	}

	var buf strings.Builder
	_, _ = fmt.Fprintf(&buf, "%s <b>TASK %s</b>\n\n", icon, label)
	_, _ = fmt.Fprintf(&buf, "<b>Task:</b> %s\n", title)
	_, _ = fmt.Fprintf(&buf, "<b>Path:</b> <code>%s</code>\n", filepath.Base(sess.CWD))

	if reason, ok := details["reason"]; ok && reason != "" {
		_, _ = fmt.Fprintf(&buf, "\n<b>Details:</b> %s\n", reason)
	}

	return buf.String()
}

func (tg *Telegram) getTaskStatusKeyboard(sessionID string, state string) map[string]interface{} {
	meta, ok := stateMeta[state]
	if !ok || meta.btn == "" {
		return nil
	}
	return map[string]interface{}{
		"inline_keyboard": [][]map[string]interface{}{
			{tgBtn(meta.btn, "act:"+meta.act+":"+sessionID)},
		},
	}
}

func ExtractShellCommand(content string) string {
	langs := []string{"bash", "sh", "shell", ""}
	type block struct {
		start int
		cmd   string
	}
	var blocks []block

	for _, lang := range langs {
		prefix := "```" + lang
		pos := 0
		for {
			if pos >= len(content) {
				break
			}
			idx := strings.Index(content[pos:], prefix)
			if idx == -1 {
				break
			}
			startIdx := pos + idx
			prefixEnd := startIdx + len(prefix)
			if prefixEnd >= len(content) {
				break
			}

			rest := content[prefixEnd:]
			lineEnd := strings.Index(rest, "\n")
			codeStart := prefixEnd
			if lineEnd != -1 {
				codeStart += lineEnd + 1
			} else if lang != "" {
				pos = startIdx + 1
				continue
			}

			if codeStart >= len(content) {
				break
			}
			endIdx := strings.Index(content[codeStart:], "```")
			if endIdx != -1 {
				cmd := strings.TrimSpace(content[codeStart : codeStart+endIdx])
				if cmd != "" {
					blocks = append(blocks, block{startIdx, cmd})
				}
				pos = codeStart + endIdx + 3
			} else {
				pos = startIdx + 1
			}
		}
	}

	if len(blocks) == 0 {
		return ""
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].start != blocks[j].start {
			return blocks[i].start < blocks[j].start
		}
		return len(blocks[i].cmd) > len(blocks[j].cmd)
	})
	return blocks[0].cmd
}

func getActionKeyboard(sessionID, content string) map[string]interface{} {
	keyboard := [][]map[string]interface{}{
		{tgBtn("➡️ Continue", "act:continue_prompt:"+sessionID), tgBtn("🆕 New Session", "act:new_session:"+sessionID)},
	}

	if cmd := ExtractShellCommand(content); cmd != "" {
		shortCmd := cmd
		if len(shortCmd) > 20 {
			shortCmd = shortCmd[:17] + "..."
		}
		keyboard = append(keyboard, []map[string]interface{}{tgBtn("▶️ Run: "+shortCmd, "act:run_command:"+sessionID)})
	}

	keyboard = append(keyboard, []map[string]interface{}{tgBtn("➕ More Actions...", "act:more_actions:"+sessionID)})
	return map[string]interface{}{"inline_keyboard": keyboard}
}

func tgBtn(text, data string) map[string]interface{} {
	return map[string]interface{}{"text": text, "callback_data": data}
}

func (tg *Telegram) getOrFocusSession(instanceID string) (*models.Session, error) {
	state, err := tg.Reg.Get(instanceID)
	if err == nil && state.SessionID != "" {
		if sess, err := tg.Sm.Load(state.SessionID); err == nil {
			return sess, nil
		}
	}

	sess, err := tg.Sm.GetLatest()
	if err != nil {
		return nil, err
	}
	tg.Reg.Set(instanceID, sess.ID)
	return sess, nil
}

func (tg *Telegram) HandleMessage(chatID int64, text string) {
	instanceID := tg.instanceID(chatID)

	if strings.HasPrefix(text, "/") {
		tg.handleCommand(chatID, instanceID, text)
		return
	}

	state, err := tg.Reg.Get(instanceID)
	if err == nil && state.PendingAction == "rename" {
		if err := tg.Sm.Rename(state.PendingData, text); err != nil {
			tg.send(chatID, "❌ Error renaming session: "+err.Error())
		} else {
			tg.send(chatID, "✅ Session renamed to: <b>"+text+"</b>")
		}
		_ = tg.Reg.ClearPending(instanceID)
		return
	}

	sess, err := tg.getOrFocusSession(instanceID)
	if err != nil {
		tg.send(chatID, "No active session. Use /sessions or /start.")
		return
	}

	if tg.Engine != nil {
		tg.dispatch(func() { tg.Engine.ExecutePrompt(sess, text) })
	} else {
		tg.send(chatID, "❌ Engine not initialized. Please contact the administrator.")
	}
}

func (tg *Telegram) handleCommand(chatID int64, instanceID, text string) {
	parts := strings.Fields(text)
	cmd := parts[0]

	switch cmd {
	case "/verbosity":
		if len(parts) > 1 {
			v := strings.ToUpper(parts[1])
			if v == "LOW" || v == "MEDIUM" || v == "HIGH" {
				tg.Reg.SetVerbosity(instanceID, v)
				tg.send(chatID, "Verbosity set to "+v)
			}
		}
	case "/yolo":
		tg.toggleYolo(chatID, instanceID)
	case "/sessions":
		tg.showSessionsMenu(chatID, 0)
	case "/start":
		tg.handleStartCommand(chatID)
	case "/run":
		if len(parts) > 1 {
			tg.startSkill(chatID, instanceID, parts[1])
		}
	case "/last":
		n := 5
		if len(parts) > 1 {
			_, _ = fmt.Sscanf(parts[1], "%d", &n)
		}
		tg.showLastLogs(chatID, instanceID, n)
	case "/help":
		tg.showHelp(chatID)
	default:
		tg.send(chatID, "Unknown command: "+cmd)
	}
}

func (tg *Telegram) showHelp(chatID int64) {
	helpText := `<b>Tenazas Help</b>
/help - Show this help message
/sessions - List and resume previous sessions
/yolo - Toggle YOLO mode (autonomous mode)
/verbosity [LOW|MEDIUM|HIGH] - Set event verbosity
/run [skill] - Run a skill from your skills folder
/last [n] - Show the last N audit log entries for the session
`
	tg.send(chatID, helpText)
}

func (tg *Telegram) handleStartCommand(chatID int64) {
	tg.send(chatID, "👋 <b>Welcome to Tenazas!</b>\n\nI am your terminal-to-Telegram gateway. What would you like to do?", map[string]interface{}{
		"reply_markup": map[string]interface{}{
			"inline_keyboard": [][]map[string]interface{}{
				{tgBtn("📂 My Sessions", "show_sessions:0")},
				{tgBtn("🛠 Run Skill", "show_skills")},
				{tgBtn("❓ Help", "help")},
			},
		},
	})
}

func (tg *Telegram) showSkillsMenu(chatID int64) {
	skills, err := tg.Sm.GetActiveSkills()
	if err != nil || len(skills) == 0 {
		tg.send(chatID, "No active skills found.")
		return
	}

	var buttons [][]map[string]interface{}
	for _, s := range skills {
		buttons = append(buttons, []map[string]interface{}{tgBtn("🚀 "+s, "skill:run:"+s)})
	}

	tg.send(chatID, "<i>Select a skill to run:</i>", map[string]interface{}{
		"reply_markup": map[string]interface{}{"inline_keyboard": buttons},
	})
}

func (tg *Telegram) toggleYolo(chatID int64, instanceID string) {
	sess, err := tg.getOrFocusSession(instanceID)
	if err != nil {
		tg.send(chatID, "No active session.")
		return
	}
	sess.Yolo = !sess.Yolo
	if err := tg.Sm.Save(sess); err != nil {
		tg.send(chatID, "❌ Error saving mode: "+err.Error())
		return
	}
	status := "OFF"
	if sess.Yolo {
		status = "ON"
	}
	tg.send(chatID, "⚠️ YOLO Mode is now <b>"+status+"</b>")
}

func (tg *Telegram) startSkill(chatID int64, instanceID, skillName string) {
	skill, err := tg.Sm.LoadSkill(skillName)
	if err != nil {
		tg.send(chatID, "Skill not found: "+err.Error())
		return
	}

	sess, err := tg.getOrFocusSession(instanceID)
	if err != nil {
		tg.send(chatID, "No session found.")
		return
	}

	sess.Title = "Task: " + skill.Name
	sess.SkillName = skillName
	if err := tg.Sm.Save(sess); err != nil {
		tg.send(chatID, "❌ Error saving session: "+err.Error())
		return
	}

	tg.send(chatID, "Running skill: <b>"+skill.Name+"</b>")
	tg.dispatch(func() { tg.Engine.Run(skill, sess) })
}

func (tg *Telegram) showLastLogs(chatID int64, instanceID string, n int) {
	sess, err := tg.getOrFocusSession(instanceID)
	if err != nil {
		tg.send(chatID, "No active session.")
		return
	}
	logs, _ := tg.Sm.GetLastAudit(sess, n)

	var buf strings.Builder
	buf.WriteString("<b>Last entries:</b>\n")
	for _, l := range logs {
		_, _ = fmt.Fprintf(&buf, "[%s] %s: %s\n", l.Timestamp.Format("15:04"), l.Type, l.Content)
	}
	tg.send(chatID, FormatHTML(buf.String()))
}

func (tg *Telegram) showSessionsMenu(chatID int64, page int) {
	pageSize := 8
	sessions, total, err := tg.Sm.ListActive(page, pageSize)
	if err != nil {
		tg.send(chatID, "Error: "+err.Error())
		return
	}

	var buttons [][]map[string]interface{}
	for _, s := range sessions {
		title := s.Title
		if title == "" {
			title = s.ID[:8]
		}
		label := fmt.Sprintf("%s (%s)", title, filepath.Base(s.CWD))
		buttons = append(buttons, []map[string]interface{}{tgBtn(label, "view_session:"+s.ID)})
	}

	var nav []map[string]interface{}
	if page > 0 {
		nav = append(nav, tgBtn("⬅️ Previous", fmt.Sprintf("show_sessions:%d", page-1)))
	}
	if (page+1)*pageSize < total {
		nav = append(nav, tgBtn("Next ➡️", fmt.Sprintf("show_sessions:%d", page+1)))
	}
	if len(nav) > 0 {
		buttons = append(buttons, nav)
	}

	tg.send(chatID, fmt.Sprintf("📂 <b>My Sessions</b> (Page %d/%d)", page+1, (total+pageSize-1)/pageSize), map[string]interface{}{
		"reply_markup": map[string]interface{}{"inline_keyboard": buttons},
	})
}

func (tg *Telegram) instanceID(chatID int64) string {
	return fmt.Sprintf("tg-%d", chatID)
}

func (tg *Telegram) HandleCallback(chatID int64, data string) {
	instanceID := tg.instanceID(chatID)
	parts := strings.Split(data, ":")
	if len(parts) == 0 || parts[0] == "" {
		return
	}
	cmd := parts[0]

	handlers := map[string]func(int64, string, []string){
		"show_sessions":     tg.handleShowSessions,
		"dir":               tg.handleShowSessions,
		"view_session":      tg.handleFocusSession,
		"res":               tg.handleFocusSession,
		"archive_session":   tg.handleArchiveSessionCB,
		"show_skills":       tg.handleShowSkills,
		"help":              tg.handleHelpCB,
		"skill":             tg.handleSkillCB,
		"intv":              tg.handleInterventionCB,
		"act":               tg.handleActionCB,
		"start_new_session": tg.handleStartNewSession,
	}

	if h, ok := handlers[cmd]; ok {
		h(chatID, instanceID, parts)
	}
}

func (tg *Telegram) handleShowSessions(chatID int64, _ string, parts []string) {
	page := 0
	if len(parts) > 1 {
		_, _ = fmt.Sscanf(parts[1], "%d", &page)
	}
	tg.showSessionsMenu(chatID, page)
}

func (tg *Telegram) handleFocusSession(chatID int64, instanceID string, parts []string) {
	if len(parts) > 1 {
		tg.focusSession(chatID, instanceID, parts[1])
	}
}

func (tg *Telegram) handleArchiveSessionCB(chatID int64, _ string, parts []string) {
	if len(parts) > 1 {
		tg.archiveSession(chatID, parts[1])
	}
}

func (tg *Telegram) handleShowSkills(chatID int64, _ string, _ []string) {
	tg.showSkillsMenu(chatID)
}

func (tg *Telegram) handleHelpCB(chatID int64, _ string, _ []string) {
	tg.showHelp(chatID)
}

func (tg *Telegram) handleSkillCB(chatID int64, instanceID string, parts []string) {
	if len(parts) > 2 && parts[1] == "run" {
		tg.startSkill(chatID, instanceID, parts[2])
	}
}

func (tg *Telegram) handleInterventionCB(chatID int64, _ string, parts []string) {
	if len(parts) == 3 {
		tg.Engine.ResolveIntervention(parts[2], parts[1])
		tg.send(chatID, "Action dispatched.")
	}
}

func (tg *Telegram) handleActionCB(chatID int64, instanceID string, parts []string) {
	tg.handleActionCallback(chatID, instanceID, parts)
}

func (tg *Telegram) handleStartNewSession(chatID int64, _ string, _ []string) {
	tg.showSessionsMenu(chatID, 0)
}

func (tg *Telegram) archiveSession(chatID int64, sessionID string) {
	if err := tg.Sm.Archive(sessionID); err != nil {
		tg.send(chatID, "❌ Error archiving: "+err.Error())
		return
	}
	tg.send(chatID, "📦 Session archived.")
}

func (tg *Telegram) handleActionCallback(chatID int64, instanceID string, parts []string) {
	if len(parts) < 3 {
		return
	}
	action, sessionID := parts[1], parts[2]

	sess, err := tg.Sm.Load(sessionID)
	if err != nil {
		tg.send(chatID, "Error: "+err.Error())
		return
	}

	actionHandlers := map[string]func(*models.Session){
		"task_pause": func(s *models.Session) {
			s.Status = models.StatusIdle
			if err := tg.Sm.Save(s); err != nil {
				tg.send(chatID, "❌ Error pausing task: "+err.Error())
			} else {
				tg.send(chatID, "⏸️ Task paused.")
			}
		},
		"task_respond": func(s *models.Session) {
			tg.Reg.Set(instanceID, s.ID)
			tg.send(chatID, "🗨️ <b>Session focused.</b> Provide input to unblock:")
		},
		"task_review": func(s *models.Session) { tg.Reg.Set(instanceID, s.ID); tg.showLastLogs(chatID, instanceID, 5) },
		"continue_prompt": func(s *models.Session) {
			if tg.Engine != nil {
				tg.dispatch(func() { tg.Engine.ExecutePrompt(s, "Continue") })
			}
		},
		"run_command": func(s *models.Session) { tg.handleRunCommandAction(chatID, s) },
		"new_session": func(s *models.Session) {
			newSess, _ := tg.Sm.Create(s.CWD, "New Session")
			newSess.Client = tg.DefaultClient
			tg.Sm.Save(newSess)
			tg.Reg.Set(instanceID, newSess.ID)
			tg.send(chatID, "🆕 Started new session in <code>"+filepath.Base(s.CWD)+"</code>")
		},
		"resume": func(s *models.Session) { tg.focusSession(chatID, instanceID, s.ID) },
		"rename": func(s *models.Session) {
			tg.Reg.SetPending(instanceID, "rename", s.ID)
			tg.send(chatID, "✏️ Enter a new title:")
		},
		"archive":      func(s *models.Session) { tg.archiveSession(chatID, s.ID) },
		"more_actions": func(s *models.Session) { tg.showMoreActions(chatID, s.ID) },
		"toggle_yolo":  func(s *models.Session) { tg.toggleYolo(chatID, instanceID) },
		"show_last":    func(s *models.Session) { tg.showLastLogs(chatID, instanceID, 5) },
		"help":         func(_ *models.Session) { tg.showHelp(chatID) },
	}

	if h, ok := actionHandlers[action]; ok {
		h(sess)
	}
}

func (tg *Telegram) showMoreActions(chatID int64, sessionID string) {
	tg.send(chatID, "<b>Session Tools:</b>", map[string]interface{}{
		"reply_markup": map[string]interface{}{
			"inline_keyboard": [][]map[string]interface{}{
				{tgBtn("📝 Rename", "act:rename:"+sessionID), tgBtn("📦 Archive", "act:archive:"+sessionID)},
				{tgBtn("⚠️ Toggle YOLO", "act:toggle_yolo:"+sessionID)},
				{tgBtn("📜 Show Last Logs", "act:show_last:"+sessionID)},
			},
		},
	})
}

func (tg *Telegram) handleRunCommandAction(chatID int64, sess *models.Session) {
	logs, _ := tg.Sm.GetLastAudit(sess, 20)
	for i := len(logs) - 1; i >= 0; i-- {
		if logs[i].Type == events.AuditLLMResponse {
			if cmd := ExtractShellCommand(logs[i].Content); cmd != "" {
				tg.dispatch(func() { tg.Engine.ExecuteCommand(sess, cmd) })
				return
			}
		}
	}
	tg.send(chatID, "No command found.")
}

func (tg *Telegram) focusSession(chatID int64, instanceID, sessID string) {
	tg.Reg.Set(instanceID, sessID)
	info := "✅ Focused on session <code>" + sessID + "</code>"
	if sess, err := tg.Sm.Load(sessID); err == nil && sess.Client != "" {
		info += "\n🔧 Client: <b>" + sess.Client + "</b>"
	}
	tg.send(chatID, info)
}

func FormatHTML(s string) string {
	return (&formatter.HtmlFormatter{}).Escape(s)
}
