//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
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

// stopProcess invia Ctrl+Break al gruppo di processo per un graceful shutdown.
// Se il processo non termina entro 5 secondi, esegue un kill forzato.
// Usa OpenProcess per ottenere un handle atomico ed eliminare la race condition TOCTOU.
func stopProcess(pid int) error {
	const (
		PROCESS_TERMINATE = 0x0001
		PROCESS_SYNCHRONIZE = 0x00100000
		STILL_ACTIVE = 259
	)
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	terminateProcess := kernel32.NewProc("TerminateProcess")
	getExitCodeProcess := kernel32.NewProc("GetExitCodeProcess")
	closeHandle := kernel32.NewProc("CloseHandle")
	generateCtrlEvent := kernel32.NewProc("GenerateConsoleCtrlEvent")

	// Ottieni un handle diretto: questo elimina il TOCTOU tra check e kill.
	handle, _, err := openProcess.Call(
		uintptr(PROCESS_TERMINATE|PROCESS_SYNCHRONIZE),
		0,
		uintptr(pid),
	)
	if handle == 0 {
		// Processo già terminato o PID non valido
		return nil
	}
	defer closeHandle.Call(handle)

	// Prova prima Ctrl+Break per graceful shutdown (5s grace period)
	r, _, _ := generateCtrlEvent.Call(1, uintptr(pid))
	if r != 0 {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var exitCode uint32
			getExitCodeProcess.Call(handle, uintptr(unsafe.Pointer(&exitCode)))
			if exitCode != STILL_ACTIVE {
				return nil // Terminato gracefully
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Grace period scaduto o Ctrl+Break non supportato: forza terminazione via handle
	r, _, err = terminateProcess.Call(handle, 1)
	if r == 0 {
		return fmt.Errorf("TerminateProcess failed: %w", err)
	}
	return nil
}

