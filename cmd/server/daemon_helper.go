package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/terenzif/ibis-assistant/internal/config"
)

const pidFileName = "ibis-assistant.pid"

func startBackgroundServer() {
	// Trova percorso file PID
	pidPath := getPidFilePath()

	// Controlla se il server è già in esecuzione
	if data, err := os.ReadFile(pidPath); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			if isProcessRunning(pid) {
				fmt.Printf("Il server è già in esecuzione con PID %d.\n", pid)
				os.Exit(0)
			}
		}
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Errore recupero eseguibile: %v\n", err)
		os.Exit(1)
	}

	// Apri o crea il file di log nella stessa directory del PID (exe dir)
	logPath := "server.log"
	if exe, err := os.Executable(); err == nil {
		logPath = filepath.Join(filepath.Dir(exe), "server.log")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Errore apertura file log %s: %v\n", logPath, err)
		os.Exit(1)
	}
	defer logFile.Close()

	// Avvia il processo in background passando l'argomento "run"
	proc, err := startBackgroundProcess(exe, []string{"run"}, logFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Errore avvio server in background: %v\n", err)
		os.Exit(1)
	}

	// Scrivi il PID nel file
	err = os.WriteFile(pidPath, []byte(strconv.Itoa(proc.Pid)), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Errore scrittura file PID: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Server avviato in background con PID %d.\nI log sono reindirizzati in server.log.\n", proc.Pid)
	os.Exit(0)
}

func stopBackgroundServer() {
	pidPath := getPidFilePath()

	data, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("Il server non è in esecuzione (file PID non trovato).")
		os.Exit(0)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		fmt.Printf("File PID corrotto. Rimozione del file %s.\n", pidFileName)
		_ = os.Remove(pidPath)
		os.Exit(0)
	}

	if !isProcessRunning(pid) {
		fmt.Printf("Il processo con PID %d non è in esecuzione. Rimozione del file %s.\n", pid, pidFileName)
		_ = os.Remove(pidPath)
		os.Exit(0)
	}

	fmt.Printf("Arresto del server (PID %d)...\n", pid)
	err = stopProcess(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Errore durante l'arresto del processo: %v\n", err)
		os.Exit(1)
	}

	_ = os.Remove(pidPath)
	fmt.Println("Server arrestato con successo.")
	os.Exit(0)
}

func getPidFilePath() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), pidFileName)
	}
	return pidFileName
}

func configExists() bool {
	if _, err := os.Stat("config.json"); err == nil {
		return true
	}
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "config.json")); err == nil {
			return true
		}
	}
	return false
}

func peekFlag(names ...string) string {
	args := os.Args
	for i := 0; i < len(args); i++ {
		arg := args[i]
		for _, name := range names {
			if arg == name {
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					return args[i+1]
				}
			}
			if strings.HasPrefix(arg, name+"=") {
				return strings.TrimPrefix(arg, name+"=")
			}
		}
	}
	return ""
}

func isPluginLaunch() bool {
	if config.IsPluginRuntime(os.Getenv("RUNTIME_MODE")) {
		return true
	}
	return config.IsPluginRuntime(peekFlag("-runtime-mode", "--runtime-mode"))
}

