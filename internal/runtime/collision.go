package runtime

import (
	"fmt"
	"net"
	"strconv"
	"time"
)

// PortInUse reports whether something is already listening on host:port.
func PortInUse(host string, port int) bool {
	if port <= 0 {
		return false
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// CollisionWarning describes a personal daemon / HTTP listener that the plugin should not attach to.
func CollisionWarning(pidRunning bool, pid int, defaultPortInUse bool, port int) string {
	switch {
	case pidRunning && defaultPortInUse:
		return fmt.Sprintf("warning: personal Ibis Assistant appears to be running (pid %d) and port %d is in use; plugin continues on stdio with its own database and will not attach to that HTTP listener", pid, port)
	case pidRunning:
		return fmt.Sprintf("warning: personal Ibis Assistant appears to be running (pid %d); plugin continues on stdio with its own database and will not attach to that process", pid)
	case defaultPortInUse:
		return fmt.Sprintf("warning: default HTTP port %d is in use (likely a personal daemon); plugin continues on stdio with its own database and will not attach to that listener", port)
	default:
		return ""
	}
}
