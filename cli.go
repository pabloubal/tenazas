package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
)

type CLI struct {
	Sm   *SessionManager
	Exec *Executor
	Reg  *Registry
}

func (c *CLI) ListAndResume() (*Session, error) {
	page := 0
	pageSize := DefaultPageSize

	for {
		sessions, total, err := c.Sm.List(page, pageSize)
		if err != nil {
			return nil, err
		}

		fmt.Printf("\x0a--- Sessions (Page %d, Total %d) ---\x0a", page+1, total)
		for i, s := range sessions {
			title := s.Title
			if title == "" {
				title = s.ID
			}
			fmt.Printf("[%d] %s (%s)\x0a", i+1, title, s.LastUpdated.Format("2006-01-02 15:04"))
		}

		fmt.Print("\x0aEnter number to resume, 'n' for next, 'p' for prev, or 'q' to quit: ")
		var input string
		fmt.Scanln(&input)

		if input == "q" {
			return nil, nil
		}
		if input == "n" && (page+1)*pageSize < total {
			page++
			continue
		}
		if input == "p" && page > 0 {
			page--
			continue
		}

		idx, err := strconv.Atoi(input)
		if err == nil && idx > 0 && idx <= len(sessions) {
			return &sessions[idx-1], nil
		}

		fmt.Println("Invalid input.")
	}
}

func (c *CLI) Run(resume bool) error {
	var sess *Session
	var err error

	if resume {
		sess, err = c.ListAndResume()
		if err != nil || sess == nil {
			return err
		}
	} else {
		cwd, _ := os.Getwd()
		sess = &Session{
			ID:          uuid.New().String(),
			CWD:         cwd,
			LastUpdated: time.Now(),
		}
		sess.EnsureLocalDir()
		c.Sm.Save(sess)
	}

	instanceID := fmt.Sprintf("cli-%d", os.Getpid())
	c.Reg.Set(instanceID, sess.ID)

	sess.EnsureLocalDir() // Ensure it exists if resumed as well

	fmt.Printf("Connected to session %s (Path: %s)\x0a", sess.ID, sess.CWD)
	fmt.Println("Type your prompt and press Enter. Ctrl+C to quit.")

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

		if sess.Title == "" {
			title := text
			if len(title) > 30 {
				title = title[:30] + "..."
			}
			sess.Title = title
			c.Sm.Save(sess)
		}

		fmt.Print("\x0aThinking...")
		err := c.Exec.Run(sess, text, func(chunk string) {
			fmt.Print(chunk)
		}, func(sid string) {
			sess.GeminiSID = sid
			c.Sm.Save(sess)
		})

		if err != nil {
			fmt.Printf("\x0aError: %v\x0a", err)
		}
		fmt.Println()
		c.Sm.Save(sess)
	}

	return nil
}
