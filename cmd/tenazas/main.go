package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"tenazas/internal/cli"
	"tenazas/internal/config"
	"tenazas/internal/engine"
	"tenazas/internal/executor"
	"tenazas/internal/heartbeat"
	"tenazas/internal/models"
	"tenazas/internal/registry"
	"tenazas/internal/session"
	"tenazas/internal/task"
	"tenazas/internal/telegram"
)

func main() {
	resume := flag.Bool("resume", false, "Resume a previous session")
	daemon := flag.Bool("daemon", false, "Run as a background daemon (Telegram bot and Heartbeat runner)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	sm := session.NewManager(cfg.StorageDir)
	reg, err := registry.NewRegistry(cfg.StorageDir)
	if err != nil {
		log.Fatalf("Failed to init registry: %v", err)
	}

	exec := executor.NewExecutor(cfg.GeminiBinPath, cfg.StorageDir)
	eng := engine.NewEngine(sm, exec, cfg.MaxLoops)

	if flag.Arg(0) == "work" {
		task.HandleWorkCommand(cfg.StorageDir, flag.Args()[1:])
		return
	}

	var tg *telegram.Telegram
	if *daemon {
		tg = setupTelegram(cfg, sm, exec, reg, eng)
		hb := heartbeat.NewRunner(cfg.StorageDir, sm, eng, tg)
		go hb.CheckAndRun()
	}

	handleSignals()

	c := cli.NewCLI(sm, exec, reg, eng)
	if err := c.Run(*resume); err != nil {
		fmt.Printf("CLI Error: %v\n", err)
	}
}

func setupTelegram(cfg *config.Config, sm *session.Manager, exec *executor.Executor, reg *registry.Registry, eng *engine.Engine) *telegram.Telegram {
	if cfg.TelegramToken == "" {
		fmt.Println("Telegram token missing, running in CLI-only mode.")
		return nil
	}

	tg := &telegram.Telegram{
		Token:          cfg.TelegramToken,
		AllowedIDs:     cfg.AllowedUserIDs,
		UpdateInterval: cfg.UpdateInterval,
		Sm:             sm,
		Exec:           exec,
		Reg:            reg,
		Engine:         eng,
	}
	go tg.Poll()
	fmt.Println("Telegram bot started.")
	return tg
}

func resumeBackgroundSessions(sm *session.Manager, eng *engine.Engine, storageDir string) {
	go func() {
		page := 0
		for {
			sessions, total, err := sm.List(page, 50)
			if err != nil || len(sessions) == 0 {
				break
			}
			for _, s := range sessions {
				if isResumable(&s) {
					if sk, err := sm.LoadSkill(s.SkillName); err == nil {
						fmt.Printf("Resuming task: %s (Skill: %s)\n", s.ID, s.SkillName)
						go eng.Run(sk, &s)
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

func isResumable(s *models.Session) bool {
	return (s.Status == models.StatusRunning || s.Status == models.StatusIntervention) && s.SkillName != ""
}

func handleSignals() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nExiting...")
		os.Exit(0)
	}()
}
