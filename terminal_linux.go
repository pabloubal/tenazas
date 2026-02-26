//go:build linux

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

// ioctl helper for terminal configuration
func ioctl(fd int, req uintptr, arg uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

func getTerminalSize() (int, int, error) {
	ws := &winsize{}
	// On Linux, TIOCGWINSZ is standard
	if err := ioctl(syscall.Stdin, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(ws))); err != nil {
		return 0, 0, err
	}
	return int(ws.Row), int(ws.Col), nil
}

func enableRawMode(fd int) (*syscall.Termios, error) {
	var old syscall.Termios
	// On Linux, TCGETS is usually used
	if err := ioctl(fd, syscall.TCGETS, uintptr(unsafe.Pointer(&old))); err != nil {
		return nil, err
	}

	raw := old
	// Apply raw mode flags. On Linux, flags are usually uint32.
	raw.Iflag &^= uint32(syscall.ICRNL | syscall.IXON)
	raw.Lflag &^= uint32(syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN)

	if err := ioctl(fd, syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); err != nil {
		return nil, err
	}

	return &old, nil
}

func restoreTerminal(fd int, oldState *syscall.Termios) error {
	if oldState == nil {
		return nil
	}
	return ioctl(fd, syscall.TCSETS, uintptr(unsafe.Pointer(oldState)))
}
