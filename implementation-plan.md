# Implementation Plan: Persistent Status Footer (PRD-002)

This plan outlines the steps to implement a persistent status footer in the Tenazas CLI. The footer will display the current execution mode, skill count, and session ID, while ensuring terminal scrolling is restricted to the region above the footer.

## 1. Data Model Updates

### 1.1 Update `Session` Struct in `models.go`
- Add `ApprovalMode string` field to the `Session` struct to track the default execution mode (e.g., "PLAN", "AUTO_EDIT").
- Default execution modes should be uppercase in the footer as requested: `[PLAN]`, `[AUTO_EDIT]`, or `[YOLO]`.

### 1.2 Update Session Initialization in `cli.go`
- In `initializeSession`, set the default `ApprovalMode` to `"PLAN"`.

## 2. Terminal Utility

### 2.1 Create `terminal.go`
Implement a utility to get the terminal size using the Go standard library (`syscall`).

```go
package main

import (
	"syscall"
	"unsafe"
)

type winsize struct {
	Row    uint16
	Col    uint16
	Xpixel uint16
	Ypixel uint16
}

func getTerminalSize() (int, int, error) {
	ws := &winsize{}
	// syscall.TIOCGWINSZ is available on Darwin and Linux
	retCode, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(syscall.Stdin),
		uintptr(syscall.TIOCGWINSZ),
		uintptr(unsafe.Pointer(ws)))

	if int(retCode) == -1 {
		return 0, 0, errno
	}
	return int(ws.Row), int(ws.Col), nil
}
```

## 3. Footer Drawing Logic in `cli.go`

### 3.1 Implement `drawFooter()`
This method will:
1. Save the cursor position using ANSI `\033[s`.
2. Move to the last line of the terminal using `\033[<Row>;1H`.
3. Clear the line using `\033[2K`.
4. Determine the current mode:
   - If `sess.Yolo` is true -> `YOLO`.
   - Else -> `sess.ApprovalMode` (defaulting to `PLAN` if empty).
5. Get the skill count using `ListSkills(c.Sm.StoragePath)`.
6. Format the footer string: `[MODE] | Skills: N | Session: ...XXXXXXX`.
7. Apply styling (e.g., white text on blue background: `\033[44;37m`).
8. Restore the cursor position using ANSI `\033[u`.

### 3.2 Implement `setupTerminal()`
This method will:
1. Get the terminal size.
2. Set the scrolling region to lines 1 to (N-1) using `\033[1;<N-1>r`.
3. Call `drawFooter()`.

## 4. Integration & Lifecycle

### 4.1 CLI Setup
- In `CLI.Run`, call `setupTerminal()` immediately after initializing the session.
- Add a `defer fmt.Fprint(c.Out, "\x1b[r")` to restore the full scrolling region on exit.

### 4.2 Signal Handling
- In `CLI.Run`, start a goroutine to listen for `syscall.SIGWINCH` (terminal resize).
- When triggered, call `setupTerminal()` to recalculate the scrolling region and redraw the footer.

### 4.3 Redraw Triggers
- Update `drawFooter()` whenever the mode changes or a new skill is added (if dynamic).
- Since `\033[r` protects the last line, normal output (LLM chunks, audit logs) won't overwrite the footer.

## 5. Execution Mode Management

### 5.1 Update `Engine.Run` and `Engine.ExecutePrompt`
- Modify `Engine` to respect `Session.ApprovalMode` if `StateDef.ApprovalMode` is not specified.
- Ensure `sess.Yolo` always takes precedence in the footer display.

### 5.2 Add `/mode` Command in `cli.go`
- Implement `/mode <plan|auto_edit|yolo>` to allow users to change the default behavior and see the footer update in real-time.

## 6. Definition of Done (DoD)

### 6.1 Functionality
- [ ] Terminal scrolling is restricted to lines 1 to N-1.
- [ ] Last line is reserved for the status footer.
- [ ] Footer displays:
    - Current mode (YOLO overrides PLAN/AUTO_EDIT).
    - Skill count from the storage directory.
    - Last 8 characters of the Session ID.
- [ ] Terminal resizing (SIGWINCH) correctly updates the scrolling region and footer position.
- [ ] Restores terminal state (full scroll region) on application exit.

### 6.2 Testing
- [ ] **Manual Test**: Run the CLI, verify the footer is visible and styled.
- [ ] **Manual Test**: Type long outputs or large numbers of commands to ensure scrolling does not affect the footer.
- [ ] **Manual Test**: Resize the terminal window and verify the footer stays at the bottom.
- [ ] **Manual Test**: Toggle `/mode` and verify the footer updates.
- [ ] **Unit Test**: Verify `getTerminalSize` returns reasonable values in a TTY environment.
- [ ] **Unit Test**: Verify the footer string formatting logic.
