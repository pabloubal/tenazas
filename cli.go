package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

type CLI struct {
	Sm     *SessionManager
	Exec   *Executor
	Reg    *Registry
	Engine *Engine
}

func (c *CLI) Run(resume bool) error {
	var sess *Session

	if resume {
		// Simplified resume for CLI, mostly rely on Telegram for rich UX
		sess, _ = c.Sm.GetLatest()
	} else {
		cwd, _ := os.Getwd()
		sess = &Session{
			ID:          uuid.New().String(),
			CWD:         cwd,
			LastUpdated: time.Now(),
			RoleCache:   make(map[string]string),
		}
		c.Sm.Save(sess)
	}

	if sess == nil {
		fmt.Println("No sessions available.")
		return nil
	}

	instanceID := fmt.Sprintf("cli-%d", os.Getpid())
	c.Reg.Set(instanceID, sess.ID)
	c.Reg.SetVerbosity(instanceID, "HIGH")

	fmt.Printf("Connected to session %s (Path: %s)\x0a", sess.ID, sess.CWD)
	fmt.Println("Commands: /run <skill>, /last <N>, /intervene <action>")

	// If session has a skill, ensure it's running
	if sess.SkillName != "" {
		skill, err := LoadSkill(c.Sm.StoragePath, sess.SkillName)
		if err == nil {
			if sess.Status != "running" && sess.Status != "intervention_required" {
				fmt.Printf("Resuming task: %s (Skill: %s)\x0a", sess.ID, sess.SkillName)
				sess.Status = "running"
				c.Sm.Save(sess)
			}
			go c.Engine.Run(skill, sess)
		}
	}

	// Subscribe to events
	eventCh := GlobalBus.Subscribe()
	go func() {
		for e := range eventCh {
			if e.SessionID == sess.ID && e.Type == EventAudit {
				audit := e.Payload.(AuditEntry)
				if audit.Type == "llm_response_chunk" {
					fmt.Print(audit.Content)
					continue
				}

				// High-visibility markers
				switch audit.Type {
				case "info":
					fmt.Printf("\x0a\x1b[34;1m🟦 %s\x1b[0m\x0a", audit.Content) // Bold Blue
				case "llm_prompt":
					fmt.Printf("\x0a\x1b[33m🟡 PROMPT (%s):\x1b[0m\x0a\x1b[90m%s\x1b[0m\x0a", audit.Source, audit.Content) // Yellow header, gray prompt
				case "llm_response":
					fmt.Printf("\x0a\x1b[32;1m🟢 RESPONSE:\x1b[0m\x0a%s\x0a", audit.Content) // Bold Green
				case "cmd_result":
					color := "32" // Green
					icon := "✅"
					if !strings.Contains(audit.Content, "Exit Code: 0") {
						color = "31" // Red
						icon = "❌"
					}
					fmt.Printf("\x0a\x1b[%s;1m%s COMMAND RESULT:\x1b[0m\x0a\x1b[90m%s\x1b[0m\x0a", color, icon, audit.Content)
				case "intervention":
					fmt.Printf("\x0a\x1b[31;1m⚠️ INTERVENTION REQUIRED:\x1b[0m\x0a%s\x0a", audit.Content)
					fmt.Print("\x0aType `/intervene <retry|proceed_to_fail|abort>`\x0a> ")
				default:
					fmt.Printf("\x0a[%s] %s\x0a", audit.Type, audit.Content)
				}
			}
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\x0a> ")
		if !scanner.Scan() {
			break
		}
		text := scanner.Text()
		if text == "" {
			continue
		}

		if strings.HasPrefix(text, "/run ") {
			skillName := strings.TrimPrefix(text, "/run ")
			skill, err := LoadSkill(c.Sm.StoragePath, skillName)
			if err != nil {
				fmt.Println("Skill error:", err)
				continue
			}
			sess.SkillName = skillName
			c.Sm.Save(sess)
			go c.Engine.Run(skill, sess)
			continue
		}

		if strings.HasPrefix(text, "/last ") {
			n := 5
			fmt.Sscanf(strings.TrimPrefix(text, "/last "), "%d", &n)
			logs, _ := c.Sm.GetLastAudit(sess, n)
			for _, l := range logs {
				fmt.Printf("[%s] %s: %s\x0a", l.Timestamp.Format("15:04"), l.Type, l.Content)
			}
			continue
		}

		if strings.HasPrefix(text, "/intervene ") {
			action := strings.TrimPrefix(text, "/intervene ")
			c.Engine.ResolveIntervention(sess.ID, action)
			continue
		}

		// Default: Execute raw prompt
		go c.Engine.ExecutePrompt(sess, text)
	}

	return nil
}
