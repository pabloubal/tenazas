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
	engine := NewEngine(sm, exec, cfg.MaxLoops)

	if flag.Arg(0) == "work" {
		HandleWorkCommand(cfg.StorageDir, flag.Args()[1:])
		return
	}

	// Start Interfaces
	tg := setupTelegram(cfg, sm, exec, reg, engine)
	hb := NewHeartbeatRunner(cfg.StorageDir, sm, engine, tg)
	go hb.CheckAndRun()

	handleSignals()

	// Run CLI
	cli := NewCLI(sm, exec, reg, engine)
	if err := cli.Run(*resume); err != nil {
		fmt.Printf("CLI Error: %v\x0a", err)
	}
}

func setupTelegram(cfg *Config, sm *SessionManager, exec *Executor, reg *Registry, engine *Engine) *Telegram {
	if cfg.TelegramToken == "" {
		fmt.Println("Telegram token missing, running in CLI-only mode.")
		return nil
	}

	tg := &Telegram{
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
	return tg
}

func resumeBackgroundSessions(sm *SessionManager, engine *Engine, storageDir string) {
	go func() {
		page := 0
		for {
			sessions, total, err := sm.List(page, 50)
			if err != nil || len(sessions) == 0 {
				break
			}
			for _, s := range sessions {
				if isResumable(&s) {
					if skill, err := LoadSkill(storageDir, s.SkillName); err == nil {
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
}

func isResumable(s *Session) bool {
	return (s.Status == StatusRunning || s.Status == StatusIntervention) && s.SkillName != ""
}

func handleSignals() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\x0aExiting...")
		os.Exit(0)
	}()
}
