//go:build darwin

package cli

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

func ioctl(fd int, req uintptr, arg uintptr) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, arg)
	if errno != 0 {
		return errno
	}
	return nil
}

func getTerminalSize() (int, int, error) {
	ws := &winsize{}
	if err := ioctl(syscall.Stdin, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(ws))); err != nil {
		return 0, 0, err
	}
	return int(ws.Row), int(ws.Col), nil
}

func enableRawMode(fd int) (*syscall.Termios, error) {
	var old syscall.Termios
	if err := ioctl(fd, syscall.TIOCGETA, uintptr(unsafe.Pointer(&old))); err != nil {
		return nil, err
	}

	raw := old
	raw.Iflag &^= uint64(syscall.ICRNL | syscall.IXON)
	raw.Lflag &^= uint64(syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN)
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if err := ioctl(fd, syscall.TIOCSETA, uintptr(unsafe.Pointer(&raw))); err != nil {
		return nil, err
	}

	return &old, nil
}

func restoreTerminal(fd int, oldState *syscall.Termios) error {
	if oldState == nil {
		return nil
	}
	return ioctl(fd, syscall.TIOCSETA, uintptr(unsafe.Pointer(oldState)))
}
