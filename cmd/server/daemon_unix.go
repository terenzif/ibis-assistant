//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// startBackgroundProcess avvia il processo in background detached su Unix/POSIX
func startBackgroundProcess(exe string, args []string, logFile *os.File) (*os.Process, error) {
	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// isProcessRunning verifica se il processo è attivo su Unix
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// stopProcess arresta il processo in modo controllato su Unix, usando SIGTERM e poi SIGKILL se necessario
func stopProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	err = proc.Signal(syscall.SIGTERM)
	if err != nil {
		return proc.Kill()
	}

	// Attendi fino a 5 secondi che il processo si spenga
	for i := 0; i < 50; i++ {
		if !isProcessRunning(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return proc.Kill()
}
