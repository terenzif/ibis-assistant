package runtime

import (
	"net"
	"strconv"
	"testing"
)

func TestPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}
	if !PortInUse("127.0.0.1", port) {
		t.Fatalf("expected port %d in use", port)
	}
	if PortInUse("127.0.0.1", 1) {
		t.Fatal("port 1 should not be treated as in use")
	}
}

func TestCollisionWarning(t *testing.T) {
	if CollisionWarning(false, 0, false, 3030) != "" {
		t.Fatal("no collision should be silent")
	}
	msg := CollisionWarning(true, 42, true, 3030)
	if msg == "" {
		t.Fatal("expected warning")
	}
}
