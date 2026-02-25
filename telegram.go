package main

import (
	"bytes"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

type Telegram struct {
	Token          string
	AllowedIDs     []int64
	UpdateInterval int // ms
	Sm             *SessionManager
	Exec           *Executor
	Reg            *Registry
	Engine         *Engine
	lastUpdateID   int64
	activeMessages map[string]*tgLiveStream // sessionID -> stream state
}

type tgLiveStream struct {
	msgID    int64
	fullText string
	lastEdit time.Time
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
	OK     bool `json:"ok"`
	Result struct {
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

func (tg *Telegram) Call(method string, payload interface{}) ([]byte, error) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/%s", tg.Token, method)
	data, _ := json.Marshal(payload)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (tg *Telegram) Poll() {
	eventCh := GlobalBus.Subscribe()
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
			if !tg.IsAllowed(upd.Message.From.ID) && !tg.IsAllowed(upd.CallbackQuery.From.ID) {
				continue
			}

			if upd.Message.Text != "" {
				go tg.HandleMessage(upd.Message.From.ID, upd.Message.Text)
			} else if upd.CallbackQuery.Data != "" {
				go tg.HandleCallback(upd.CallbackQuery.From.ID, upd.CallbackQuery.Data)
			}
		}
	}
}

func (tg *Telegram) listenEvents(ch chan Event) {
	formatter := &HtmlFormatter{}
	for e := range ch {
		if e.Type == EventAudit {
			tg.broadcastAudit(e.SessionID, e.Payload.(AuditEntry), formatter)
		}
	}
}

func (tg *Telegram) broadcastAudit(sessionID string, audit AuditEntry, f *HtmlFormatter) {
	if tg.activeMessages == nil {
		tg.activeMessages = make(map[string]*tgLiveStream)
	}

	for _, id := range tg.AllowedIDs {
		instanceID := fmt.Sprintf("tg-%d", id)
		state, err := tg.Reg.Get(instanceID)
		if err != nil || state.SessionID != sessionID {
			continue
		}

		// Handle Streaming vs Discrete events
		switch audit.Type {
		case AuditLLMChunk:
			tg.handleStreamingChunk(id, sessionID, audit.Content, f)
		case AuditLLMResponse:
			tg.handleStreamingEnd(id, sessionID, audit.Content, f)
		default:
			if tg.shouldDisplay(state.Verbosity, audit.Type) {
				tg.sendAuditMessage(id, sessionID, audit, f)
			}
		}
	}
}

func (tg *Telegram) shouldDisplay(verbosity, auditType string) bool {
	switch verbosity {
	case "LOW":
		return auditType == AuditIntervention || auditType == AuditStatus
	case "MEDIUM":
		return auditType == AuditIntervention || auditType == AuditInfo || auditType == AuditStatus
	case "HIGH":
		return true
	}
	return false
}

func (tg *Telegram) sendAuditMessage(chatID int64, sessionID string, audit AuditEntry, f *HtmlFormatter) {
	prefix, suffix := "", ""
	if sess, _ := tg.Sm.Load(sessionID); sess != nil && sess.Yolo {
		prefix, suffix = "⚠️ <b>YOLO MODE</b> ⚠️\x0a\x0a", "\x0a\x0a⚠️ <b>YOLO MODE</b> ⚠️"
	}

	content := f.Format(audit)
	if audit.Type == AuditIntervention {
		tg.sendIntervention(chatID, sessionID, prefix+content+suffix)
	} else {
		tg.Call("sendMessage", map[string]interface{}{
			"chat_id":    chatID,
			"text":       prefix + content + suffix,
			"parse_mode": "HTML",
		})
	}
}

func (tg *Telegram) handleStreamingChunk(id int64, sessionID, content string, f *HtmlFormatter) {
	stream, ok := tg.activeMessages[sessionID]
	if !ok {
		msgData, _ := tg.Call("sendMessage", map[string]interface{}{
			"chat_id":    id,
			"text":       "<i>...</i>",
			"parse_mode": "HTML",
		})
		var msgRes TgMessageResponse
		json.Unmarshal(msgData, &msgRes)
		stream = &tgLiveStream{msgID: msgRes.Result.MessageID, lastEdit: time.Now()}
		tg.activeMessages[sessionID] = stream
	}
	
	stream.fullText += content
	if time.Since(stream.lastEdit) > time.Duration(tg.UpdateInterval)*time.Millisecond {
		tg.Call("editMessageText", map[string]interface{}{
			"chat_id":    id,
			"message_id": stream.msgID,
			"text":       f.escape(stream.fullText),
			"parse_mode": "HTML",
		})
		stream.lastEdit = time.Now()
	}
}

func (tg *Telegram) handleStreamingEnd(id int64, sessionID, content string, f *HtmlFormatter) {
	if stream, ok := tg.activeMessages[sessionID]; ok {
		tg.Call("editMessageText", map[string]interface{}{
			"chat_id":    id,
			"message_id": stream.msgID,
			"text":       "🟢 <b>RESPONSE:</b>\x0a" + f.escape(content),
			"parse_mode": "HTML",
		})
		delete(tg.activeMessages, sessionID)
	} else {
		tg.Call("sendMessage", map[string]interface{}{
			"chat_id":    id,
			"text":       "🟢 <b>RESPONSE:</b>\x0a" + f.escape(content),
			"parse_mode": "HTML",
		})
	}
}

func (tg *Telegram) sendIntervention(id int64, sessionID, text string) {
	tg.Call("sendMessage", map[string]interface{}{
		"chat_id":    id,
		"text":       text,
		"parse_mode": "HTML",
		"reply_markup": map[string]interface{}{
			"inline_keyboard": [][]map[string]interface{}{
				{
					{"text": "🔄 Retry", "callback_data": "intv:retry:" + sessionID},
					{"text": "⏩ Proceed", "callback_data": "intv:proceed_to_fail:" + sessionID},
				},
				{
					{"text": "🛑 Abort", "callback_data": "intv:abort:" + sessionID},
				},
			},
		},
	})
}

func (tg *Telegram) NotifyWithButton(chatID int64, text, btnText, callbackData string) {
	tg.Call("sendMessage", map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
		"reply_markup": map[string]interface{}{
			"inline_keyboard": [][]map[string]interface{}{
				{{"text": btnText, "callback_data": callbackData}},
			},
		},
	})
}

func (tg *Telegram) HandleMessage(chatID int64, text string) {
	instanceID := fmt.Sprintf("tg-%d", chatID)

	if strings.HasPrefix(text, "/") {
		tg.handleCommand(chatID, instanceID, text)
		return
	}

	state, err := tg.Reg.Get(instanceID)
	if err != nil {
		tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "No active session."})
		return
	}

	sess, err := tg.Sm.Load(state.SessionID)
	if err != nil {
		tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Session load failed."})
		return
	}

	go tg.Engine.ExecutePrompt(sess, text)
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
				tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Verbosity set to " + v})
			}
		}
	case "/yolo":
		tg.toggleYolo(chatID, instanceID)
	case "/resume":
		tg.showResumeMenu(chatID)
	case "/run":
		if len(parts) > 1 {
			tg.startSkill(chatID, instanceID, parts[1])
		}
	case "/last":
		n := 5
		if len(parts) > 1 {
			fmt.Sscanf(parts[1], "%d", &n)
		}
		tg.showLastLogs(chatID, instanceID, n)
	default:
		tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Unknown command: " + cmd})
	}
}

func (tg *Telegram) toggleYolo(chatID int64, instanceID string) {
	state, err := tg.Reg.Get(instanceID)
	if err != nil { return }
	sess, _ := tg.Sm.Load(state.SessionID)
	sess.Yolo = !sess.Yolo
	tg.Sm.Save(sess)
	status := "OFF"
	if sess.Yolo { status = "ON" }
	tg.Call("sendMessage", map[string]interface{}{
		"chat_id": chatID, 
		"text": "⚠️ YOLO Mode is now <b>" + status + "</b>", 
		"parse_mode": "HTML",
	})
}

func (tg *Telegram) startSkill(chatID int64, instanceID, skillName string) {
	skill, err := LoadSkill(tg.Sm.StoragePath, skillName)
	if err != nil {
		tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Skill not found: " + err.Error()})
		return
	}
	
	state, _ := tg.Reg.Get(instanceID)
	sess, _ := tg.Sm.Load(state.SessionID)
	sess.Title = "Task: " + skill.Name
	sess.SkillName = skillName
	tg.Sm.Save(sess)
	
	tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Running skill: <b>" + skill.Name + "</b>", "parse_mode": "HTML"})
	go tg.Engine.Run(skill, sess)
}

func (tg *Telegram) showLastLogs(chatID int64, instanceID string, n int) {
	state, _ := tg.Reg.Get(instanceID)
	sess, _ := tg.Sm.Load(state.SessionID)
	logs, _ := tg.Sm.GetLastAudit(sess, n)
	
	f := &HtmlFormatter{}
	var buf strings.Builder
	buf.WriteString("<b>Last entries:</b>\x0a")
	for _, l := range logs {
		buf.WriteString(fmt.Sprintf("[%s] %s: %s\x0a", l.Timestamp.Format("15:04"), l.Type, l.Content))
	}
	tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": f.escape(buf.String()), "parse_mode": "HTML"})
}

func (tg *Telegram) showResumeMenu(chatID int64) {
	sessions, _, _ := tg.Sm.List(0, 50)
	cwds := make(map[string]bool)
	var buttons [][]map[string]interface{}
	
	for _, s := range sessions {
		if !cwds[s.CWD] {
			cwds[s.CWD] = true
			hash := fmt.Sprintf("%x", md5.Sum([]byte(s.CWD)))[:8]
			buttons = append(buttons, []map[string]interface{}{
				{"text": "📂 " + filepath.Base(s.CWD), "callback_data": "dir:" + hash},
			})
		}
	}

	tg.Call("sendMessage", map[string]interface{}{
		"chat_id":      chatID,
		"text":         "<i>Select Workspace:</i>",
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": buttons},
	})
}

func (tg *Telegram) HandleCallback(chatID int64, data string) {
	instanceID := fmt.Sprintf("tg-%d", chatID)
	
	switch {
	case strings.HasPrefix(data, "dir:"):
		tg.showSessionsInDir(chatID, data[4:])
	case strings.HasPrefix(data, "res:"):
		tg.focusSession(chatID, instanceID, data[4:])
	case strings.HasPrefix(data, "intv:"):
		tg.resolveIntervention(chatID, data)
	}
}

func (tg *Telegram) showSessionsInDir(chatID int64, hash string) {
	sessions, _, _ := tg.Sm.List(0, 50)
	var buttons [][]map[string]interface{}
	
	for _, s := range sessions {
		h := fmt.Sprintf("%x", md5.Sum([]byte(s.CWD)))[:8]
		if h == hash {
			title := s.Title
			if title == "" { title = s.ID[:8] }
			buttons = append(buttons, []map[string]interface{}{
				{"text": "📑 " + title, "callback_data": "res:" + s.ID},
			})
		}
	}
	tg.Call("sendMessage", map[string]interface{}{
		"chat_id":      chatID,
		"text":         "<i>Select Session:</i>",
		"parse_mode":   "HTML",
		"reply_markup": map[string]interface{}{"inline_keyboard": buttons},
	})
}

func (tg *Telegram) focusSession(chatID int64, instanceID, sessID string) {
	tg.Reg.Set(instanceID, sessID)
	tg.Call("sendMessage", map[string]interface{}{
		"chat_id":    chatID,
		"text":       "✅ Focused on session <code>" + sessID + "</code>",
		"parse_mode": "HTML",
	})
}

func (tg *Telegram) resolveIntervention(chatID int64, data string) {
	parts := strings.Split(data, ":")
	if len(parts) == 3 {
		tg.Engine.ResolveIntervention(parts[2], parts[1])
		tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Action '" + parts[1] + "' dispatched."})
	}
}

// formatHTML is a legacy helper maintained for test compatibility.
func formatHTML(s string) string {
	f := &HtmlFormatter{}
	return f.escape(s)
}
