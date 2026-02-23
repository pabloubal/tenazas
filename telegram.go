package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type TgUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  struct {
		MessageID int64 `json:"message_id"`
		From      struct {
			ID int64 `json:"id"`
		} `json:"from"`
		Text    string `json:"text"`
		Caption string `json:"caption"`
		Photo   []struct {
			FileID   string `json:"file_id"`
			FileSize int64  `json:"file_size"`
		} `json:"photo"`
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

type Telegram struct {
	Token          string
	AllowedIDs     []int64
	UpdateInterval int // ms
	Sm             *SessionManager
	Exec           *Executor
	Reg            *Registry
	lastUpdateID   int64
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

func (tg *Telegram) DownloadFile(fileID string, destDir string) (string, error) {
	// 1. Get file path via getFile
	res, err := tg.Call("getFile", map[string]interface{}{"file_id": fileID})
	if err != nil {
		return "", err
	}
	var fileRes struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.Unmarshal(res, &fileRes); err != nil {
		return "", err
	}
	if !fileRes.OK {
		return "", fmt.Errorf("telegram getFile failed: %s", string(res))
	}

	// 2. Download the actual file
	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", tg.Token, fileRes.Result.FilePath)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}

	// 3. Save to the session's local directory
	tmpFile, err := os.CreateTemp(destDir, "tenazas-*.jpg")
	if err != nil {
		return "", err
	}
	defer tmpFile.Close()

	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {
		os.Remove(tmpFile.Name())
		return "", err
	}

	return tmpFile.Name(), nil
}

func (tg *Telegram) Poll() {
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

			if upd.Message.MessageID != 0 {
				if !tg.IsAllowed(upd.Message.From.ID) {
					continue
				}

				if upd.Message.Text != "" {
					go tg.HandleMessage(upd.Message.From.ID, upd.Message.Text, "")
				} else if len(upd.Message.Photo) > 0 {
					// Pick the largest photo
					largest := upd.Message.Photo[0]
					for _, p := range upd.Message.Photo {
						if p.FileSize > largest.FileSize {
							largest = p
						}
					}

					// Look up session first to get local dir
					chatID := upd.Message.From.ID
					instanceID := fmt.Sprintf("tg-%d", chatID)
					sessID, err := tg.Reg.Get(instanceID)
					var sess *Session
					if err != nil {
						sess, err = tg.Sm.GetLatest()
					} else {
						sess, _ = tg.Sm.Load(sessID)
					}

					if sess == nil {
						tg.Call("sendMessage", map[string]interface{}{
							"chat_id": chatID,
							"text":    "⚠️ No active session found. Start one on CLI first.",
						})
						continue
					}

					localDir, err := sess.EnsureLocalDir()
					if err != nil {
						tg.Call("sendMessage", map[string]interface{}{
							"chat_id": chatID,
							"text":    "❌ Error creating local storage: " + err.Error(),
						})
						continue
					}

					go func(chatID int64, fileID, caption string, destDir string) {
						path, err := tg.DownloadFile(fileID, destDir)
						if err != nil {
							tg.Call("sendMessage", map[string]interface{}{
								"chat_id": chatID,
								"text":    "❌ Error downloading image: " + err.Error(),
							})
							return
						}
						tg.HandleMessage(chatID, caption, path)
					}(chatID, largest.FileID, upd.Message.Caption, localDir)
				}
			} else if upd.CallbackQuery.Data != "" {
				if !tg.IsAllowed(upd.CallbackQuery.From.ID) {
					continue
				}
				go tg.HandleCallback(upd.CallbackQuery.From.ID, upd.CallbackQuery.Data)
			}
		}
	}
}

func (tg *Telegram) HandleMessage(chatID int64, text string, imagePath string) {
	if imagePath != "" {
		defer os.Remove(imagePath)
	}
	if text == "/start" || text == "/help" {
		tg.Call("sendMessage", map[string]interface{}{
			"chat_id":    chatID,
			"text":       "<b>Tenazas Gateway</b>\x0aUse /resume to pick a session.\x0aUse /yolo to toggle auto-approve mode.\x0a\x0a<i>Images are supported!</i> Send any image to the bot.",
			"parse_mode": "HTML",
		})
		return
	}

	if text == "/resume" {
		sessions, _, _ := tg.Sm.List(0, 10)
		var buttons [][]map[string]interface{}
		for _, s := range sessions {
			title := s.Title
			if title == "" {
				title = s.ID
			}
			buttons = append(buttons, []map[string]interface{}{
				{"text": title, "callback_data": "res:" + s.ID},
			})
		}
		tg.Call("sendMessage", map[string]interface{}{
			"chat_id":      chatID,
			"text":         "<i>Pick a session to resume:</i>",
			"parse_mode":   "HTML",
			"reply_markup": map[string]interface{}{"inline_keyboard": buttons},
		})
		return
	}

	// Default: use latest or assigned
	instanceID := fmt.Sprintf("tg-%d", chatID)
	sessID, err := tg.Reg.Get(instanceID)
	var sess *Session
	if err != nil {
		sess, err = tg.Sm.GetLatest()
		if err != nil {
			tg.Call("sendMessage", map[string]interface{}{
				"chat_id":    chatID,
				"text":       "⚠️ No active session found. Start one on CLI first.",
				"parse_mode": "HTML",
			})
			return
		}
		tg.Reg.Set(instanceID, sess.ID)
	} else {
		sess, _ = tg.Sm.Load(sessID)
	}

	if text == "/yolo" {
		sess.Yolo = !sess.Yolo
		tg.Sm.Save(sess)
		status := "<b>OFF</b>"
		if sess.Yolo {
			status = "<b>ON</b>"
		}
		tg.Call("sendMessage", map[string]interface{}{
			"chat_id":    chatID,
			"text":       "⚠️ YOLO Mode (Auto-Approve) is now " + status,
			"parse_mode": "HTML",
		})
		return
	}

	tg.ProcessPrompt(chatID, sess, text, imagePath)
}

func (tg *Telegram) HandleCallback(chatID int64, data string) {
	if len(data) > 4 && data[:4] == "res:" {
		sessID := data[4:]
		instanceID := fmt.Sprintf("tg-%d", chatID)
		tg.Reg.Set(instanceID, sessID)
		tg.Call("sendMessage", map[string]interface{}{
			"chat_id":    chatID,
			"text":       "✅ Resumed session <code>" + sessID + "</code>",
			"parse_mode": "HTML",
		})
	}
}

func (tg *Telegram) ProcessPrompt(chatID int64, sess *Session, text string, imagePath string) {
	msgData, _ := tg.Call("sendMessage", map[string]interface{}{
		"chat_id":    chatID,
		"text":       "<i>Thinking...</i>",
		"parse_mode": "HTML",
	})
	var msgRes TgMessageResponse
	json.Unmarshal(msgData, &msgRes)
	msgID := msgRes.Result.MessageID

	var fullText string
	lastEdit := time.Now()

	prefix := ""
	suffix := ""
	if sess.Yolo {
		prefix = "⚠️ <b>YOLO MODE ACTIVE</b> ⚠️\x0a\x0a"
		suffix = "\x0a\x0a⚠️ <b>YOLO MODE ACTIVE</b> ⚠️"
	}

	prompt := text
	if imagePath != "" {
		if prompt != "" {
			prompt += " @" + imagePath
		} else {
			prompt = "Analyze this image: @" + imagePath
		}
	}

	tg.Exec.Run(sess, prompt, func(chunk string) {
		fullText += chunk
		if time.Since(lastEdit) > time.Duration(tg.UpdateInterval)*time.Millisecond {
			tg.Call("editMessageText", map[string]interface{}{
				"chat_id":    chatID,
				"message_id": msgID,
				"text":       prefix + formatHTML(fullText),
				"parse_mode": "HTML",
			})
			lastEdit = time.Now()
		}
	}, func(sid string) {
		sess.GeminiSID = sid
		tg.Sm.Save(sess)
	})

	// Final edit
	tg.Call("editMessageText", map[string]interface{}{
		"chat_id":    chatID,
		"message_id": msgID,
		"text":       prefix + formatHTML(fullText) + suffix,
		"parse_mode": "HTML",
	})
	tg.Sm.Save(sess)
}

func formatHTML(s string) string {
	// 1. Escape HTML special chars
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")

	// 2. Simple Markdown conversions (non-exhaustive but covers the basics)
	// Bold: **text** -> <b>text</b>
	for strings.Contains(s, "**") {
		s = strings.Replace(s, "**", "<b>", 1)
		s = strings.Replace(s, "**", "</b>", 1)
	}
	// Code block: ```text``` -> <pre>text</pre>
	for strings.Contains(s, "```") {
		s = strings.Replace(s, "```", "<pre>", 1)
		s = strings.Replace(s, "```", "</pre>", 1)
	}
	// Inline Code: `text` -> <code>text</code>
	for strings.Contains(s, "`") {
		s = strings.Replace(s, "`", "<code>", 1)
		s = strings.Replace(s, "`", "</code>", 1)
	}
	// Italic: *text* -> <i>text</i>
	// Note: We avoid simple "_" to prevent breaking file paths.
	for strings.Contains(s, "*") {
		s = strings.Replace(s, "*", "<i>", 1)
		s = strings.Replace(s, "*", "</i>", 1)
	}

	return s
}
