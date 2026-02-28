package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"tenazas/internal/cli"
	"tenazas/internal/client"
	_ "tenazas/internal/client" // register client implementations
	"tenazas/internal/config"
	"tenazas/internal/engine"
	"tenazas/internal/events"
	"tenazas/internal/formatter"
	"tenazas/internal/heartbeat"
	"tenazas/internal/models"
	"tenazas/internal/onboard"
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

	// Handle subcommands that don't need full initialization
	if flag.Arg(0) == "onboard" {
		if err := onboard.Run(cfg.StorageDir); err != nil {
			log.Fatalf("Onboard failed: %v", err)
		}
		return
	}

	sm := session.NewManager(cfg.StorageDir)
	reg, err := registry.NewRegistry(cfg.StorageDir)
	if err != nil {
		log.Fatalf("Failed to init registry: %v", err)
	}

	logPath := filepath.Join(cfg.StorageDir, "tenazas.log")
	clients := make(map[string]client.Client)
	for name, cc := range cfg.Clients {
		c, cerr := client.NewClient(name, cc.BinPath, logPath)
		if cerr != nil {
			log.Printf("Warning: could not init client %q: %v", name, cerr)
			continue
		}
		if len(cc.Models) > 0 {
			c.SetModels(cc.Models)
		}
		clients[name] = c
	}
	eng := engine.NewEngine(sm, clients, cfg.DefaultClient, cfg.MaxLoops)

	if flag.Arg(0) == "work" {
		task.HandleWorkCommand(cfg.StorageDir, flag.Args()[1:])
		return
	}

	if flag.Arg(0) == "run" {
		if flag.Arg(1) == "" {
			fmt.Println("Usage: tenazas run <skillname>")
			os.Exit(1)
		}
		handleSignals()
		os.Exit(handleRunCommand(sm, eng, cfg, flag.Arg(1)))
	}

	var tg *telegram.Telegram
	if *daemon {
		if cfg.Channel.Type == "telegram" {
			tg = setupTelegram(cfg, sm, reg, eng)
		}
		hb := heartbeat.NewRunner(cfg.StorageDir, sm, eng, tg)
		go hb.CheckAndRun()
	}

	handleSignals()

	clientModels := make(map[string]map[string]string)
	for name, cc := range cfg.Clients {
		if len(cc.Models) > 0 {
			clientModels[name] = cc.Models
		}
	}

	c := cli.NewCLI(sm, reg, eng, cfg.DefaultClient, cfg.DefaultModelTier, clientModels)
	if err := c.Run(*resume); err != nil {
		fmt.Printf("CLI Error: %v\n", err)
	}
}

func setupTelegram(cfg *config.Config, sm *session.Manager, reg *registry.Registry, eng *engine.Engine) *telegram.Telegram {
	if cfg.Channel.Token == "" {
		fmt.Println("Telegram token missing, running in CLI-only mode.")
		return nil
	}

	tg := &telegram.Telegram{
		Token:          cfg.Channel.Token,
		AllowedIDs:     cfg.Channel.AllowedUserIDs,
		UpdateInterval: cfg.Channel.UpdateInterval,
		Sm:             sm,
		Reg:            reg,
		Engine:         eng,
		DefaultClient:  cfg.DefaultClient,
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

func handleRunCommand(sm *session.Manager, eng *engine.Engine, cfg *config.Config, skillName string) int {
	cwd, _ := os.Getwd()

	sess, err := sm.Create(cwd, "run: "+skillName)
	if err != nil {
		fmt.Printf("Failed to create session: %v\n", err)
		return 1
	}
	sess.Client = cfg.DefaultClient
	sess.SkillName = skillName
	sess.Yolo = true
	if cfg.DefaultModelTier != "" {
		sess.ModelTier = cfg.DefaultModelTier
	}
	sm.Save(sess)

	sk, err := sm.LoadSkill(skillName)
	if err != nil {
		fmt.Printf("Failed to load skill %q: %v\n", skillName, err)
		return 1
	}

	// Stream events to stdout.
	eventCh := events.GlobalBus.Subscribe()
	f := &formatter.AnsiFormatter{}
	done := make(chan struct{})

	go func() {
		defer close(done)
		for e := range eventCh {
			if e.SessionID != sess.ID || e.Type != events.EventAudit {
				continue
			}
			audit, ok := e.Payload.(events.AuditEntry)
			if !ok {
				continue
			}
			switch audit.Type {
			case events.AuditLLMChunk:
				fmt.Print(audit.Content)
			case events.AuditLLMResponse:
				fmt.Println()
			case events.AuditCmdResult, events.AuditStatus, events.AuditInfo, events.AuditIntervention:
				fmt.Println(f.Format(audit))
			}
		}
	}()

	eng.Run(sk, sess)

	events.GlobalBus.Unsubscribe(eventCh)
	<-done

	// Reload to get final status.
	if updated, err := sm.Load(sess.ID); err == nil {
		sess = updated
	}

	if sess.Status == models.StatusCompleted {
		return 0
	}
	return 1
}
