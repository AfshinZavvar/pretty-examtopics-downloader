//go:build windows

package main

import "syscall"

const enableVirtualTerminalProcessing = 0x0004

var setConsoleMode = syscall.NewLazyDLL("kernel32.dll").NewProc("SetConsoleMode")

// enableVirtualTerminal turns ANSI colour processing on for classic Windows
// Console hosts. Windows Terminal already supports it, and accepts the same
// mode flag. If stdout is redirected or the mode cannot be changed, the caller
// falls back to plain output.
func enableVirtualTerminal() bool {
	handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil || handle == syscall.InvalidHandle {
		return false
	}
	var mode uint32
	if err := syscall.GetConsoleMode(handle, &mode); err != nil {
		return false
	}
	result, _, _ := setConsoleMode.Call(uintptr(handle), uintptr(mode|enableVirtualTerminalProcessing))
	return result != 0
}
