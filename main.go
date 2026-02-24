package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	resume := flag.Bool("resume", false, "Resume a previous session")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	sm := NewSessionManager(cfg.StorageDir)
	reg, err := NewRegistry(cfg.StorageDir)
	if err != nil {
		log.Fatalf("Failed to init registry: %v", err)
	}

	exec := NewExecutor(cfg.GeminiBinPath, cfg.StorageDir)
	engine := NewEngine(sm, exec)

	// Start Telegram
	var tg *Telegram
	if cfg.TelegramToken != "" {
		tg = &Telegram{
			Token:          cfg.TelegramToken,
			AllowedIDs:     cfg.AllowedUserIDs,
			UpdateInterval: cfg.UpdateInterval,
			Sm:             sm,
			Exec:           exec,
			Reg:            reg,
			Engine:         engine,
		}
		go tg.Poll()
		fmt.Println("Telegram bot started.")
	} else {
		fmt.Println("Telegram token missing, running in CLI-only mode.")
	}

	// Start Heartbeat Runner
	hb := NewHeartbeatRunner(cfg.StorageDir, sm, engine, tg)
	go hb.CheckAndRun() // In prod, this would run on a ticker/cron.

	// Resume any sessions marked as "running" or "intervention_required"
	go func() {
		page := 0
		for {
			sessions, total, err := sm.List(page, 50)
			if err != nil || len(sessions) == 0 {
				break
			}
			for _, s := range sessions {
				if (s.Status == "running" || s.Status == "intervention_required") && s.SkillName != "" {
					skill, err := LoadSkill(cfg.StorageDir, s.SkillName)
					if err == nil {
						fmt.Printf("Resuming task: %s (Skill: %s)\x0a", s.ID, s.SkillName)
						go engine.Run(skill, &s)
					}
				}
			}
			if (page+1)*50 >= total {
				break
			}
			page++
		}
	}()

	// Handle signals for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\x0aExiting...")
		os.Exit(0)
	}()

	// Start CLI
	cli := &CLI{
		Sm:     sm,
		Exec:   exec,
		Reg:    reg,
		Engine: engine,
	}

	if err := cli.Run(*resume); err != nil {
		fmt.Printf("CLI Error: %v\x0a", err)
	}
}
