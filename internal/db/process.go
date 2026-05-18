package db

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// ProcessManager handles the lifecycle of the embedded SurrealDB process.
type ProcessManager struct {
	cmd *exec.Cmd
}

// StartEmbedded launches the surreal process in the background.
// It expects the binary to be named 'surreal.exe' (Windows) or 'surreal' (Unix) in the current directory.
func StartEmbedded(user, password, dataPath string, port int, autoUpdate bool) (*ProcessManager, error) {
	binName := getSurrealBinPath()

	// Ensure binary exists and is up to date
	if err := EnsureSurrealDB(autoUpdate); err != nil {
		return nil, fmt.Errorf("failed to ensure database binary: %w", err)
	}

	// Construct command
	// surreal start --user root --pass root --bind 127.0.0.1:8000 surrealkv:C:\path\to\db
	bindAddr := fmt.Sprintf("127.0.0.1:%d", port)

	absDataPath, err := filepath.Abs(dataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for data: %w", err)
	}
	// Using surrealkv as per manual change, but with absolute path.
	fileArg := fmt.Sprintf("surrealkv:%s", absDataPath)

	args := []string{
		"start",
		"--user", user,
		"--pass", password,
		"--bind", bindAddr,
		fileArg,
	}

	absBinPath, err := filepath.Abs(binName)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for database binary: %w", err)
	}

	cmd := exec.Command(absBinPath, args...)

	// Check if log file exists/create it
	logPath := "surreal.log"
	if exe, err := os.Executable(); err == nil {
		logPath = filepath.Join(filepath.Dir(exe), "surreal.log")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		// Fallback to stdio if log file fails
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}

	log.Printf("Starting embedded database: %s %v", binName, args)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start database process: %w", err)
	}

	// Wait for port to be open
	if err := waitForPort(port, 10*time.Second); err != nil {
		// If timeout, try to kill and return error
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("database started but port %d did not open in time: %w", port, err)
	}

	return &ProcessManager{cmd: cmd}, nil
}

// Stop attempts to gracefully shut down the database.
func (pm *ProcessManager) Stop() error {
	if pm.cmd == nil || pm.cmd.Process == nil {
		return nil
	}

	log.Println("Stopping embedded database...")

	// Attempt graceful shutdown via Signal (only on non-Windows)
	if runtime.GOOS != "windows" {
		if err := pm.cmd.Process.Signal(os.Interrupt); err != nil {
			// If we can't signal (e.g. process already dead), just return
			// But on Windows this returns "not supported", which we now avoid.
			log.Printf("Warning: Failed to signal database: %v", err)
		}
	} else {
		// On Windows, if started via terminal, Ctrl+C propagates.
		// If not, we have no easy way to SIGINT without external tools.
		// We'll proceed to Wait, and if that fails, we Kill.
	}

	// Wait for exit with timeout
	done := make(chan error, 1)
	go func() {
		done <- pm.cmd.Wait()
	}()

	select {
	case <-done:
		log.Println("Embedded database stopped.")
		return nil
	case <-time.After(2 * time.Second):
		// Use shorter timeout for dev responsiveness
		log.Println("Database did not stop in time (or requires Kill on Windows), forcing kill...")
		return pm.cmd.Process.Kill()
	}
}

func waitForPort(port int, timeout time.Duration) error {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("timeout connecting to port")
}
