package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type HeartbeatRunner struct {
	configDir string
	sm        *SessionManager
	engine    *Engine
	tg        *Telegram
}

func NewHeartbeatRunner(configDir string, sm *SessionManager, engine *Engine, tg *Telegram) *HeartbeatRunner {
	return &HeartbeatRunner{
		configDir: configDir,
		sm:        sm,
		engine:    engine,
		tg:        tg,
	}
}

// In a real implementation this would use croner or a ticker.
// We provide a simple scanner to simulate execution of scheduled heartbeats.
func (h *HeartbeatRunner) CheckAndRun() {
	files, err := os.ReadDir(filepath.Join(h.configDir, "heartbeats"))
	if err != nil {
		return
	}

	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			data, err := os.ReadFile(filepath.Join(h.configDir, "heartbeats", f.Name()))
			if err != nil {
				continue
			}

			var hb Heartbeat
			if err := json.Unmarshal(data, &hb); err != nil {
				continue
			}

			// Dummy trigger condition for demo purposes.
			// Normally you'd parse hb.Interval and check last run time.
			go h.Trigger(hb)
		}
	}
}

func (h *HeartbeatRunner) Trigger(hb Heartbeat) {
	skill, err := LoadSkill(h.configDir, hb.Skill)
	if err != nil {
		return
	}

	sess := &Session{
		ID:          uuid.New().String(),
		CWD:         hb.Path,
		Title:       "Heartbeat: " + hb.Name,
		SkillName:   hb.Skill,
		LastUpdated: time.Now(),
		Status:      "idle",
		RoleCache:   make(map[string]string),
	}
	h.sm.Save(sess)

	if h.tg != nil {
		msg := fmt.Sprintf("Beep! 🤖 My heartbeat noticed a trigger for <b>%s</b>. I am spinning up a background session to investigate.\x0aPath: <code>%s</code>", hb.Name, hb.Path)
		for _, aid := range h.tg.AllowedIDs {
			h.tg.NotifyWithButton(aid, msg, "👁️ Watch Session", "res:"+sess.ID)
		}
	}

	h.engine.Run(skill, sess)
	
	if h.tg != nil {
		msg := fmt.Sprintf("🏁 Heartbeat <b>%s</b> has finished with status: <b>%s</b>", hb.Name, sess.Status)
		for _, aid := range h.tg.AllowedIDs {
			h.tg.NotifyWithButton(aid, msg, "🔍 Review Log", "res:"+sess.ID)
		}
	}
}
