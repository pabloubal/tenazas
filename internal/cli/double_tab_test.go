package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"tenazas/internal/models"
	"tenazas/internal/session"
)

func TestCLI_DoubleTabTrigger(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-doubletab-test-*")
	defer os.RemoveAll(tmpDir)

	sm := session.NewManager(tmpDir)
	var out bytes.Buffer
	cli := NewCLI(sm, nil, nil, "gemini", "", nil)
	cli.Out = &out
	sess := &models.Session{
		ID:           uuid.New().String(),
		CWD:          tmpDir,
		ApprovalMode: models.ApprovalModePlan,
		Yolo:         false,
	}
	cli.sess = sess

	// Mock completions
	cli.input = []rune("/r")
	// /run is a possible completion for /r

	// Case 1: Fast Double Tab
	// Input: Tab, Tab, Ctrl-C
	cli.In = strings.NewReader("		\x03")
	out.Reset()
	cli.Out = &out
	cli.IsImmersive = false

	err := cli.replRaw(sess)
	if err != nil && err != io.EOF {
		t.Errorf("unexpected error: %v", err)
	}

	if !cli.IsImmersive {
		t.Error("expected IsImmersive to be true after fast double tab")
	}

	// Case 2: Slow Double Tab
	cli.In = strings.NewReader("	")
	cli.IsImmersive = false
	cli.lastTabTime = time.Time{}

	// We can't easily test slow double tab in a single replRaw call without a custom reader
	// that introduces delays. Let's try that.
}

type delayedReader struct {
	data   []string
	delays []time.Duration
	idx    int
}

func (r *delayedReader) Read(p []byte) (n int, err error) {
	if r.idx >= len(r.data) {
		return 0, io.EOF
	}
	if r.idx < len(r.delays) {
		time.Sleep(r.delays[r.idx])
	}
	n = copy(p, r.data[r.idx])
	r.idx++
	return n, nil
}

func TestCLI_DoubleTabComprehensive(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-doubletab-test-*")
	defer os.RemoveAll(tmpDir)

	sm := session.NewManager(tmpDir)
	var out bytes.Buffer
	cli := NewCLI(sm, nil, nil, "gemini", "", nil)
	cli.Out = &out
	sess := &models.Session{
		ID:           uuid.New().String(),
		CWD:          tmpDir,
		ApprovalMode: models.ApprovalModePlan,
	}
	cli.sess = sess

	t.Run("Fast Double Tab Toggles", func(t *testing.T) {
		cli.IsImmersive = false
		cli.lastTabTime = time.Time{}
		cli.In = &delayedReader{
			data:   []string{"	", "	", "\x03"},
			delays: []time.Duration{0, 50 * time.Millisecond, 0},
		}
		cli.replRaw(sess)
		if !cli.IsImmersive {
			t.Error("expected IsImmersive to be true after fast double tab")
		}
	})

	t.Run("Slow Double Tab Does Not Toggle", func(t *testing.T) {
		cli.IsImmersive = false
		cli.lastTabTime = time.Time{}
		cli.In = &delayedReader{
			data:   []string{"	", "	", "\x03"},
			delays: []time.Duration{0, 400 * time.Millisecond, 0},
		}
		cli.replRaw(sess)
		if cli.IsImmersive {
			t.Error("expected IsImmersive to be false after slow double tab")
		}
	})

	t.Run("Interrupted Tab Resets Timer", func(t *testing.T) {
		cli.IsImmersive = false
		cli.lastTabTime = time.Time{}
		// Tab, 'a', Tab (all fast)
		cli.In = &delayedReader{
			data:   []string{"	", "a", "	", "\x03"},
			delays: []time.Duration{0, 10 * time.Millisecond, 10 * time.Millisecond, 0},
		}
		cli.replRaw(sess)
		if cli.IsImmersive {
			t.Error("expected IsImmersive to be false when interrupted by another key")
		}
	})

	t.Run("Exclusivity: Second Tab Does Not Cycle Completion", func(t *testing.T) {
		cli.IsImmersive = false
		cli.lastTabTime = time.Time{}
		cli.input = []rune("/")
		// Completions for "/" include "/run", "/last", "/intervene", etc.
		// 1st Tab should set input to "/run" (index 0)
		// 2nd Tab (if it cycled) would set input to "/last" (index 1)
		cli.In = &delayedReader{
			data:   []string{"	", "	", "\x03"},
			delays: []time.Duration{0, 50 * time.Millisecond, 0},
		}
		cli.replRaw(sess)

		if string(cli.input) != "/run" {
			t.Errorf("expected input to remain '/run' after double tab toggle, got '%s'", string(cli.input))
		}
	})

	t.Run("Triple Tab Logic", func(t *testing.T) {
		cli.IsImmersive = false
		cli.lastTabTime = time.Time{}
		cli.input = []rune("/")
		// 1st Tab: Completion index 0 (/run)
		// 2nd Tab: Toggle Immersive, index stays 0
		// 3rd Tab: Completion index 1 (/last)
		cli.In = &delayedReader{
			data:   []string{"	", "	", "	", "\x03"},
			delays: []time.Duration{0, 50 * time.Millisecond, 50 * time.Millisecond, 0},
		}
		cli.replRaw(sess)

		if !cli.IsImmersive {
			t.Error("expected IsImmersive to be true")
		}
		if string(cli.input) != "/last" {
			t.Errorf("expected input to be '/last' after triple tab, got '%s'", string(cli.input))
		}
	})
}
