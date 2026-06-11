//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// startBackgroundProcess avvia il processo in background detached su Windows
func startBackgroundProcess(exe string, args []string, logFile *os.File) (*os.Process, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// DETACHED_PROCESS = 0x00000008
		// CREATE_NEW_PROCESS_GROUP = 0x00000200
		CreationFlags: 0x00000008 | 0x00000200,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// isProcessRunning verifica se il processo è attivo su Windows
func isProcessRunning(pid int) bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getProcessVersion := kernel32.NewProc("GetProcessVersion")
	r, _, _ := getProcessVersion.Call(uintptr(pid))
	return r != 0
}

// stopProcess termina forzatamente il processo su Windows
func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}
