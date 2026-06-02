//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func setupTerminal(in *os.File, out io.Writer) (func(), error) {
	fd := int(in.Fd())
	original, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, fmt.Errorf("get terminal mode: %w", err)
	}

	raw := *original
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &raw); err != nil {
		return nil, fmt.Errorf("set terminal raw mode: %w", err)
	}

	return func() {
		_ = unix.IoctlSetTermios(fd, unix.TIOCSETA, original)
		fmt.Fprint(out, "\x1b[?25h")
	}, nil
}
