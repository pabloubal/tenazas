// Package main implements Tenazas, a high-performance, zero-dependency gateway
// for the gemini CLI. It bridges terminal and Telegram interfaces with
// stateful session handoff and directory-aware reasoning.
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
	resume := flag.Bool("resume", false, "Resume a previous session (CLI only)")
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

	command := "cli"
	if len(flag.Args()) > 0 {
		command = flag.Arg(0)
	}

	// Handle signals for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\x0aExiting...")
		os.Exit(0)
	}()

	switch command {
	case "server":
		if cfg.TelegramToken == "" {
			log.Fatal("Telegram token missing in config")
		}
		tg := &Telegram{
			Token:          cfg.TelegramToken,
			AllowedIDs:     cfg.AllowedUserIDs,
			UpdateInterval: cfg.UpdateInterval,
			Sm:             sm,
			Exec:           exec,
			Reg:            reg,
		}
		fmt.Println("Starting Telegram server...")
		tg.Poll() // Poll is a blocking loop

	case "cli":
		cli := &CLI{
			Sm:   sm,
			Exec: exec,
			Reg:  reg,
		}
		if err := cli.Run(*resume); err != nil {
			fmt.Printf("CLI Error: %v\x0a", err)
		}

	default:
		fmt.Printf("Unknown command: %s\x0aUsage: tenazas [cli|server]\x0a", command)
		os.Exit(1)
	}
}
