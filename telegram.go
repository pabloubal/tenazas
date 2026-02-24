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

// ... basic structs
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
		json.Unmarshal(data, &res)
		for _, upd := range res.Result {
			tg.lastUpdateID = upd.UpdateID
			if upd.Message.Text != "" {
				if !tg.IsAllowed(upd.Message.From.ID) {
					continue
				}
				go tg.HandleMessage(upd.Message.From.ID, upd.Message.Text)
			} else if upd.CallbackQuery.Data != "" {
				if !tg.IsAllowed(upd.CallbackQuery.From.ID) {
					continue
				}
				go tg.HandleCallback(upd.CallbackQuery.From.ID, upd.CallbackQuery.Data)
			}
		}
	}
}

func (tg *Telegram) listenEvents(ch chan Event) {
	for e := range ch {
		if e.Type == EventAudit {
			audit := e.Payload.(AuditEntry)
			tg.broadcastAudit(e.SessionID, audit)
		}
	}
}

func (tg *Telegram) broadcastAudit(sessionID string, audit AuditEntry) {
	if tg.activeMessages == nil {
		tg.activeMessages = make(map[string]*tgLiveStream)
	}

	// Find users focused on this session
	for _, id := range tg.AllowedIDs {
		instanceID := fmt.Sprintf("tg-%d", id)
		state, err := tg.Reg.Get(instanceID)
		if err != nil || state.SessionID != sessionID {
			continue
		}

		// Handle live streaming chunks
		if audit.Type == "llm_response_chunk" {
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

			stream.fullText += audit.Content
			if time.Since(stream.lastEdit) > time.Duration(tg.UpdateInterval)*time.Millisecond {
				tg.Call("editMessageText", map[string]interface{}{
					"chat_id":    id,
					"message_id": stream.msgID,
					"text":       formatHTML(stream.fullText),
					"parse_mode": "HTML",
				})
				stream.lastEdit = time.Now()
			}
			continue
		}

		// Handle final response
		if audit.Type == "llm_response" {
			if stream, ok := tg.activeMessages[sessionID]; ok {
				tg.Call("editMessageText", map[string]interface{}{
					"chat_id":    id,
					"message_id": stream.msgID,
					"text":       "🟢 <b>RESPONSE:</b>\x0a" + formatHTML(audit.Content),
					"parse_mode": "HTML",
				})
				delete(tg.activeMessages, sessionID)
			} else {
				tg.Call("sendMessage", map[string]interface{}{
					"chat_id":    id,
					"text":       "🟢 <b>RESPONSE:</b>\x0a" + formatHTML(audit.Content),
					"parse_mode": "HTML",
				})
			}
			continue
		}

		// Verbosity Check for other events
		if state.Verbosity == "LOW" && audit.Type != "intervention" && audit.Type != "status" {
			continue
		}
		if state.Verbosity == "MEDIUM" && audit.Type != "intervention" && audit.Type != "info" {
			continue
		}

		prefix := ""
		suffix := ""
		sess, _ := tg.Sm.Load(sessionID)
		if sess.Yolo {
			prefix = "⚠️ <b>YOLO MODE ACTIVE</b> ⚠️\x0a\x0a"
			suffix = "\x0a\x0a⚠️ <b>YOLO MODE ACTIVE</b> ⚠️"
		}

		content := formatHTML(audit.Content)
		switch audit.Type {
		case "info":
			if strings.HasPrefix(audit.Content, "Started skill") || strings.HasPrefix(audit.Content, "Running") || strings.HasPrefix(audit.Content, "Executing") {
				content = "🟦 <b>" + content + "</b>"
			} else {
				content = "ℹ️ <i>" + content + "</i>"
			}
		case "llm_prompt":
			content = "🟡 <b>PROMPT (" + audit.Source + "):</b>\x0a<code>" + content + "</code>"
		case "cmd_result":
			icon := "✅"
			if !strings.Contains(audit.Content, "Exit Code: 0") {
				icon = "❌"
			}
			content = icon + " <b>COMMAND RESULT:</b>\x0a<pre>" + content + "</pre>"
		case "intervention":
			tg.Call("sendMessage", map[string]interface{}{
				"chat_id":    id,
				"text":       prefix + "⚠️ <b>Intervention Required</b>\x0a" + content + suffix,
				"parse_mode": "HTML",
				"reply_markup": map[string]interface{}{
					"inline_keyboard": [][]map[string]interface{}{
						{
							{"text": "🔄 Retry", "callback_data": "intv:retry:" + sessionID},
							{"text": "⏩ Fail Route", "callback_data": "intv:fail_route:" + sessionID},
						},
						{
							{"text": "🛑 Abort", "callback_data": "intv:abort:" + sessionID},
						},
					},
				},
			})
			continue
		}

		tg.Call("sendMessage", map[string]interface{}{
			"chat_id":    id,
			"text":       prefix + content + suffix,
			"parse_mode": "HTML",
		})
	}
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

	if strings.HasPrefix(text, "/verbosity ") {
		v := strings.ToUpper(strings.TrimPrefix(text, "/verbosity "))
		if v == "LOW" || v == "MEDIUM" || v == "HIGH" {
			tg.Reg.SetVerbosity(instanceID, v)
			tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Verbosity set to " + v})
		}
		return
	}

	if text == "/yolo" {
		state, err := tg.Reg.Get(instanceID)
		if err == nil {
			sess, _ := tg.Sm.Load(state.SessionID)
			sess.Yolo = !sess.Yolo
			tg.Sm.Save(sess)
			status := "<b>OFF</b>"
			if sess.Yolo {
				status = "<b>ON</b>"
			}
			tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "⚠️ YOLO Mode is now " + status, "parse_mode": "HTML"})
		}
		return
	}

	if text == "/resume" {
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
		return
	}

	// Active session handler
	state, err := tg.Reg.Get(instanceID)
	if err != nil {
		tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "No active session."})
		return
	}

	if strings.HasPrefix(text, "/run ") {
		skillName := strings.TrimPrefix(text, "/run ")
		skill, err := LoadSkill(tg.Sm.StoragePath, skillName)
		if err != nil {
			tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Skill not found: " + err.Error()})
			return
		}
		sess, _ := tg.Sm.Load(state.SessionID)
		sess.Title = "Task: " + skill.Name
		sess.SkillName = skillName
		tg.Sm.Save(sess)
		tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Running skill " + skill.Name})
		go tg.Engine.Run(skill, sess)
		return
	}

	if strings.HasPrefix(text, "/last ") {
		nStr := strings.TrimPrefix(text, "/last ")
		n := 5
		fmt.Sscanf(nStr, "%d", &n)
		sess, _ := tg.Sm.Load(state.SessionID)
		logs, _ := tg.Sm.GetLastAudit(sess, n)
		
		var buf bytes.Buffer
		buf.WriteString("<b>Last entries:</b>\x0a")
		for _, l := range logs {
			buf.WriteString(fmt.Sprintf("[%s] %s: %s\x0a", l.Timestamp.Format("15:04"), l.Type, l.Content))
		}
		tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": formatHTML(buf.String()), "parse_mode": "HTML"})
		return
	}

	// Just a raw message, treat as info/prompt context injection
	sess, _ := tg.Sm.Load(state.SessionID)
	tg.Sm.AppendAudit(sess, AuditEntry{
		Type:    "user_input",
		Source:  "telegram",
		Content: text,
	})
}

func (tg *Telegram) HandleCallback(chatID int64, data string) {
	instanceID := fmt.Sprintf("tg-%d", chatID)
	
	if strings.HasPrefix(data, "dir:") {
		hash := data[4:]
		sessions, _, _ := tg.Sm.List(0, 50)
		var buttons [][]map[string]interface{}
		
		for _, s := range sessions {
			h := fmt.Sprintf("%x", md5.Sum([]byte(s.CWD)))[:8]
			if h == hash {
				title := s.Title
				if title == "" {
					title = s.ID[:8]
				}
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
		return
	}

	if strings.HasPrefix(data, "res:") {
		sessID := data[4:]
		tg.Reg.Set(instanceID, sessID)
		tg.Call("sendMessage", map[string]interface{}{
			"chat_id":    chatID,
			"text":       "✅ Focused on session <code>" + sessID + "</code>",
			"parse_mode": "HTML",
		})
		return
	}

	if strings.HasPrefix(data, "intv:") {
		parts := strings.Split(data, ":")
		if len(parts) == 3 {
			action := parts[1]
			sessID := parts[2]
			tg.Engine.ResolveIntervention(sessID, action)
			tg.Call("sendMessage", map[string]interface{}{"chat_id": chatID, "text": "Action '" + action + "' dispatched."})
		}
	}
}

func formatHTML(s string) string {
	// 1. Escape HTML special chars
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")

	// 2. Bold: **text** -> <b>text</b>
	for strings.Count(s, "**") >= 2 {
		s = strings.Replace(s, "**", "<b>", 1)
		s = strings.Replace(s, "**", "</b>", 1)
	}

	// 3. Code block: ```text``` -> <pre>text</pre>
	for strings.Count(s, "```") >= 2 {
		s = strings.Replace(s, "```", "<pre>", 1)
		s = strings.Replace(s, "```", "</pre>", 1)
	}

	// 4. Inline Code: `text` -> <code>text</code>
	for strings.Count(s, "`") >= 2 {
		s = strings.Replace(s, "`", "<code>", 1)
		s = strings.Replace(s, "`", "</code>", 1)
	}

	// 5. Italic: *text* -> <i>text</i>
	for strings.Count(s, "*") >= 2 {
		s = strings.Replace(s, "*", "<i>", 1)
		s = strings.Replace(s, "*", "</i>", 1)
	}

	if len(s) > 3500 {
		s = s[:3500] + "...[TRUNCATED]"
	}
	return s
}
