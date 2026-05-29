//go:build windows

package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

func setupTerminal(in *os.File, out io.Writer) (func(), error) {
	inputHandle := windows.Handle(in.Fd())

	var originalInput uint32
	if err := windows.GetConsoleMode(inputHandle, &originalInput); err != nil {
		return nil, fmt.Errorf("get console input mode: %w", err)
	}

	rawMode := originalInput
	rawMode &^= windows.ENABLE_ECHO_INPUT
	rawMode &^= windows.ENABLE_LINE_INPUT
	rawMode &^= windows.ENABLE_PROCESSED_INPUT
	if err := windows.SetConsoleMode(inputHandle, rawMode); err != nil {
		return nil, fmt.Errorf("set console input mode: %w", err)
	}

	outputHandle := windows.Handle(os.Stdout.Fd())
	var originalOutput uint32
	outputModeSet := false
	if err := windows.GetConsoleMode(outputHandle, &originalOutput); err == nil {
		outputMode := originalOutput | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
		if err := windows.SetConsoleMode(outputHandle, outputMode); err != nil {
			_ = windows.SetConsoleMode(inputHandle, originalInput)
			return nil, fmt.Errorf("set console output mode: %w", err)
		}
		outputModeSet = true
	}

	return func() {
		_ = windows.SetConsoleMode(inputHandle, originalInput)
		if outputModeSet {
			_ = windows.SetConsoleMode(outputHandle, originalOutput)
		}
		fmt.Fprint(out, "\x1b[?25h")
	}, nil
}
