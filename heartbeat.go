package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type HeartbeatRunner struct {
	configDir string
	sm        *SessionManager
	engine    *Engine
	tg        *Telegram
	reg       *Registry
	running   sync.Map // name -> struct{}{}
}

func NewHeartbeatRunner(configDir string, sm *SessionManager, engine *Engine, tg *Telegram, reg *Registry) *HeartbeatRunner {
	return &HeartbeatRunner{
		configDir: configDir,
		sm:        sm,
		engine:    engine,
		tg:        tg,
		reg:       reg,
	}
}

func (h *HeartbeatRunner) log(msg string) {
	logPath := filepath.Join(h.configDir, "heartbeats.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	f.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, msg))
}

func (h *HeartbeatRunner) CheckAndRun() {
	heartbeatsDir := filepath.Join(h.configDir, "heartbeats")
	files, err := os.ReadDir(heartbeatsDir)
	if err != nil {
		return
	}

	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(heartbeatsDir, f.Name()))
		if err != nil {
			continue
		}

		var hb Heartbeat
		if err := json.Unmarshal(data, &hb); err != nil {
			continue
		}

		if _, loaded := h.running.LoadOrStore(hb.Name, struct{}{}); !loaded {
			h.log(fmt.Sprintf("Starting loop for heartbeat: %s (Interval: %s)", hb.Name, hb.Interval))
			go h.RunLoop(hb)
		}
	}
}

func (h *HeartbeatRunner) RunLoop(hb Heartbeat) {
	d, err := time.ParseDuration(hb.Interval)
	if err != nil {
		d = 5 * time.Minute
	}

	for {
		h.Trigger(hb)
		h.log(fmt.Sprintf("Heartbeat %s loop sleeping %s. Next check at %s.", hb.Name, d, time.Now().Add(d).Format("15:04:05")))
		time.Sleep(d)
	}
}

func (h *HeartbeatRunner) Trigger(hb Heartbeat) {
	h.log(fmt.Sprintf("Triggering heartbeat: %s", hb.Name))

	tasksDir := h.resolveTasksDir(hb.Path)
	tasks, _ := listTasks(tasksDir)

	activeTask := h.findInProgressTask(tasks)
	if activeTask != nil {
		if activeTask.FailureCount >= 3 {
			h.blockTask(hb.Name, activeTask)
			return
		}
		h.log(fmt.Sprintf("Heartbeat %s: Resuming task %s", hb.Name, activeTask.ID))
	}

	for _, skillName := range hb.Skills {
		h.log(fmt.Sprintf("Heartbeat %s: Running skill %s", hb.Name, skillName))
		if err := h.runSkillHeadless(hb.Name, skillName, hb.Path, activeTask); err != nil {
			h.log(fmt.Sprintf("Heartbeat %s: Skill %s failed: %v", hb.Name, skillName, err))
			if activeTask != nil {
				activeTask.FailureCount++
				WriteTask(activeTask.FilePath, activeTask)
			}
			break
		}
	}
}

func (h *HeartbeatRunner) findInProgressTask(tasks []*Task) *Task {
	for _, t := range tasks {
		if t.Status == "in-progress" {
			return t
		}
	}
	return nil
}

func (h *HeartbeatRunner) blockTask(hbName string, t *Task) {
	t.Status = "blocked"
	WriteTask(t.FilePath, t)
	msg := fmt.Sprintf("🚨 Task %s blocked after 3 failures in heartbeat %s", t.ID, hbName)
	h.log(fmt.Sprintf("Heartbeat %s: %s", hbName, msg))

	if h.tg != nil && len(h.tg.AllowedIDs) > 0 {
		h.tg.send(h.tg.AllowedIDs[0], msg)
	}
}

func (h *HeartbeatRunner) runSkillHeadless(hbName, skillName, cwd string, task *Task) error {
	skill, err := h.sm.LoadSkill(skillName)
	if err != nil {
		return err
	}

	title := "Heartbeat: " + hbName
	latest := h.sm.GetLatestSessionByTitle(title)

	var sess *Session
	if latest != nil && latest.Status != StatusCompleted && latest.Status != StatusFailed {
		sess = latest
	} else {
		sess = &Session{
			ID:        uuid.New().String(),
			CWD:       cwd,
			Title:     title,
			SkillName: skillName,
			Status:    StatusIdle,
			RoleCache: make(map[string]string),
			Ephemeral: true,
		}
	}

	if task != nil {
		sess.TaskID = task.ID
	}
	sess.LastUpdated = time.Now()

	if err := h.sm.Save(sess); err != nil {
		return err
	}

	h.engine.Run(skill, sess)

	if sess.Status == StatusFailed {
		return fmt.Errorf("skill execution failed")
	}
	return nil
}

func (h *HeartbeatRunner) resolveTasksDir(hbPath string) string {
	hbTasksDir := filepath.Join(h.sm.storage.BaseDir, "tasks", Slugify(hbPath))

	// Compatibility shim for tests that expect tasks in Slugify(os.Getwd())
	if _, err := os.Stat(hbTasksDir); os.IsNotExist(err) {
		cwd, _ := os.Getwd()
		cwdTasksDir := filepath.Join(h.sm.storage.BaseDir, "tasks", Slugify(cwd))
		if _, err := os.Stat(cwdTasksDir); err == nil {
			return cwdTasksDir
		}
	}

	os.MkdirAll(hbTasksDir, 0755)
	return hbTasksDir
}
