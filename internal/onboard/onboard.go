package onboard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"tenazas/internal/config"
)

// knownClients maps display name → binary name for LookPath.
var knownClients = []struct {
	Name string
	Bin  string
}{
	{"gemini", "gemini"},
	{"claude-code", "claude"},
}

type detectedClient struct {
	Name    string
	Bin     string
	Path    string
	Found   bool
}

// Run executes the interactive onboarding wizard and writes config.json.
func Run(storageDir string) error {
	fmt.Println("\n  Welcome to Tenazas Setup!")

	// --- Step 1: detect clients ---
	fmt.Println("  Scanning for installed clients...")
	var detected []detectedClient
	for _, kc := range knownClients {
		path, err := exec.LookPath(kc.Bin)
		dc := detectedClient{Name: kc.Name, Bin: kc.Bin}
		if err == nil {
			dc.Path = path
			dc.Found = true
			fmt.Printf("  ✓ %s found at %s\n", kc.Name, path)
		} else {
			fmt.Printf("  ✗ %s not found\n", kc.Name)
		}
		detected = append(detected, dc)
	}
	fmt.Println()

	// Filter to found clients
	var available []detectedClient
	for _, d := range detected {
		if d.Found {
			available = append(available, d)
		}
	}

	// --- Step 2: select default client ---
	var defaultClient string
	clients := make(map[string]config.ClientConfig)

	if len(available) == 0 {
		fmt.Println("  No supported clients found.")
		fmt.Print("  Enter path to a client binary: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			p := strings.TrimSpace(scanner.Text())
			if p == "" {
				return fmt.Errorf("no client binary provided")
			}
			defaultClient = "custom"
			clients["custom"] = config.ClientConfig{BinPath: p}
		}
	} else if len(available) == 1 {
		sel := available[0]
		fmt.Printf("  Auto-selected %s (only client found)\n\n", sel.Name)
		defaultClient = sel.Name
		clients[sel.Name] = config.ClientConfig{BinPath: sel.Path}
	} else {
		var clientItems []menuItem
		for _, dc := range available {
			clientItems = append(clientItems, menuItem{Label: dc.Name, Desc: "(" + dc.Path + ")"})
		}
		idx, err := selectMenu("  Select your default client:", clientItems)
		if err != nil {
			return fmt.Errorf("client selection: %w", err)
		}
		defaultClient = available[idx].Name
		clients[available[idx].Name] = config.ClientConfig{BinPath: available[idx].Path}
		fmt.Println()
	}

	// --- Step 3: customize paths for other detected clients ---
	scanner := bufio.NewScanner(os.Stdin)
	for _, dc := range available {
		if _, ok := clients[dc.Name]; ok {
			continue
		}
		fmt.Printf("  Custom path for %s? (Enter to keep %s): ", dc.Name, dc.Path)
		if scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				clients[dc.Name] = config.ClientConfig{BinPath: line}
			} else {
				clients[dc.Name] = config.ClientConfig{BinPath: dc.Path}
			}
		}
	}

	// --- Step 4: select communication channel ---
	channelOptions := []menuItem{
		{Label: "Telegram", Desc: "Receive notifications and control sessions via Telegram bot"},
		{Label: "Disabled", Desc: "No external communication channel"},
	}
	chIdx, err := selectMenu("  Select external communication channel:", channelOptions)
	if err != nil {
		return fmt.Errorf("channel selection: %w", err)
	}
	fmt.Println()

	channel := "disabled"
	var tgToken string
	var allowedIDs []int64

	if chIdx == 0 {
		channel = "telegram"

		// --- Step 5: Telegram token ---
		fmt.Print("  Telegram Bot Token: ")
		if scanner.Scan() {
			tgToken = strings.TrimSpace(scanner.Text())
		}
		if tgToken == "" {
			fmt.Println("  ⚠ No token provided — Telegram will not be active until configured.")
		}

		// --- Step 6: allowed user IDs ---
		if tgToken != "" {
			fmt.Print("  Allowed Telegram User IDs (comma-separated): ")
			if scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line != "" {
					for _, s := range strings.Split(line, ",") {
						s = strings.TrimSpace(s)
						if id, err := strconv.ParseInt(s, 10, 64); err == nil {
							allowedIDs = append(allowedIDs, id)
						}
					}
				}
			}
		}
	}

	// --- Step 7: build config ---
	geminiBinPath := "gemini"
	if gc, ok := clients["gemini"]; ok {
		geminiBinPath = gc.BinPath
	}

	cfg := &config.Config{
		StorageDir:     storageDir,
		TelegramToken:  tgToken,
		AllowedUserIDs: allowedIDs,
		UpdateInterval: config.DefaultTgInterval,
		GeminiBinPath:  geminiBinPath,
		MaxLoops:       config.DefaultMaxLoops,
		DefaultClient:  defaultClient,
		Clients:        clients,
		Channel:        channel,
	}

	// --- Step 8: create directories ---
	for _, sub := range []string{"", "sessions", "tasks", "skills", "heartbeats"} {
		if err := os.MkdirAll(filepath.Join(storageDir, sub), 0755); err != nil {
			return fmt.Errorf("creating directory: %w", err)
		}
	}

	// --- Step 9: write config ---
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	cfgPath := filepath.Join(storageDir, config.ConfigFileName)
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	fmt.Printf("\n  ✓ Config saved to %s\n", cfgPath)
	return nil
}

// menuItem represents a choice in the interactive menu.
type menuItem struct {
	Label string
	Desc  string
}

// selectMenu displays an interactive arrow-key menu and returns the selected index.
func selectMenu(title string, items []menuItem) (int, error) {
	fmt.Println(title)
	selected := 0
	for {
		printMenu(items, selected)

		if err := rawModeOn(); err != nil {
			return 0, fmt.Errorf("enabling raw mode: %w", err)
		}

		key, err := readKey()
		rawModeOff()

		if err != nil {
			return 0, fmt.Errorf("reading key: %w", err)
		}

		switch key {
		case keyUp:
			if selected > 0 {
				selected--
			}
		case keyDown:
			if selected < len(items)-1 {
				selected++
			}
		case keyEnter:
			return selected, nil
		}

		fmt.Printf("\033[%dA", len(items))
	}
}

func printMenu(items []menuItem, selected int) {
	for i, item := range items {
		if i == selected {
			fmt.Printf("  › %-14s%s\n", item.Label, item.Desc)
		} else {
			fmt.Printf("    %-14s%s\n", item.Label, item.Desc)
		}
	}
}

type keyCode int

const (
	keyUp    keyCode = iota
	keyDown
	keyEnter
	keyOther
)

func readKey() (keyCode, error) {
	buf := make([]byte, 3)
	n, err := os.Stdin.Read(buf)
	if err != nil {
		return keyOther, err
	}
	if n == 1 {
		switch buf[0] {
		case 13, 10: // Enter
			return keyEnter, nil
		case 'k':
			return keyUp, nil
		case 'j':
			return keyDown, nil
		}
	}
	// Escape sequence: ESC [ A/B
	if n >= 3 && buf[0] == 27 && buf[1] == '[' {
		switch buf[2] {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		}
	}
	return keyOther, nil
}

func rawModeOn() error {
	cmd := exec.Command("stty", "raw", "-echo")
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func rawModeOff() {
	cmd := exec.Command("stty", "-raw", "echo")
	cmd.Stdin = os.Stdin
	cmd.Run()
}
