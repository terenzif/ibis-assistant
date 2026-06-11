package logs

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/terenzif/ibis-arc/internal/config"
)

func startMockSMTPServer(t *testing.T) (net.Listener, chan string) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock smtp server: %v", err)
	}

	commandsChan := make(chan string, 100)

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				writer := bufio.NewWriter(c)
				reader := bufio.NewReader(c)

				writer.WriteString("220 mock-smtp-server\r\n")
				writer.Flush()

				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.TrimSpace(line)
					commandsChan <- cmd

					if strings.HasPrefix(strings.ToUpper(cmd), "EHLO") || strings.HasPrefix(strings.ToUpper(cmd), "HELO") {
						writer.WriteString("250-mock-smtp-server\r\n250 AUTH PLAIN\r\n")
						writer.Flush()
					} else if strings.HasPrefix(strings.ToUpper(cmd), "MAIL FROM:") {
						writer.WriteString("250 2.1.0 OK\r\n")
						writer.Flush()
					} else if strings.HasPrefix(strings.ToUpper(cmd), "RCPT TO:") {
						writer.WriteString("250 2.1.5 OK\r\n")
						writer.Flush()
					} else if cmd == "DATA" {
						writer.WriteString("354 Start mail input\r\n")
						writer.Flush()
						for {
							dataLine, err := reader.ReadString('\n')
							if err != nil {
								return
							}
							trimmed := strings.TrimSpace(dataLine)
							if trimmed == "." {
								break
							}
							if trimmed == "" {
								continue
							}
							commandsChan <- "DATA: " + trimmed
						}
						writer.WriteString("250 2.0.0 OK\r\n")
						writer.Flush()
					} else if cmd == "QUIT" {
						writer.WriteString("221 2.0.0 Bye\r\n")
						writer.Flush()
						return
					} else if strings.HasPrefix(strings.ToUpper(cmd), "AUTH PLAIN") {
						writer.WriteString("235 2.7.0 Authentication successful\r\n")
						writer.Flush()
					} else {
						writer.WriteString("250 OK\r\n")
						writer.Flush()
					}
				}
			}(conn)
		}
	}()

	return l, commandsChan
}

func TestSendEmailNoAuth(t *testing.T) {
	l, cmds := startMockSMTPServer(t)
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:    true,
			Host:       "127.0.0.1",
			Port:       port,
			Encryption: "none",
			From:       "test-sender@example.com",
			To:         "test-receiver@example.com",
		},
	}

	notifier := NewNotifier(cfg, nil)
	err := notifier.SendRawEmail([]byte("Subject: Test Subject\r\n\r\nHello World"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Verify the commands received by the mock server
	expectedCmds := []string{
		"EHLO localhost",
		"MAIL FROM:<test-sender@example.com>",
		"RCPT TO:<test-receiver@example.com>",
		"DATA",
		"DATA: Subject: Test Subject",
		"DATA: Hello World",
		"QUIT",
	}

	for _, expected := range expectedCmds {
		select {
		case cmd := <-cmds:
			if !strings.Contains(cmd, expected) && !strings.Contains(expected, cmd) && cmd != "EHLO 127.0.0.1" {
				t.Errorf("expected cmd containing %q, got %q", expected, cmd)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for command: %s", expected)
		}
	}
}

func TestSendEmailWithAuth(t *testing.T) {
	l, cmds := startMockSMTPServer(t)
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:    true,
			Host:       "127.0.0.1",
			Port:       port,
			Encryption: "none",
			User:       "user1",
			Password:   "secret",
			From:       "test-sender@example.com",
			To:         "test-receiver@example.com",
		},
	}

	notifier := NewNotifier(cfg, nil)
	err := notifier.SendRawEmail([]byte("Subject: Test Subject\r\n\r\nHello World"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Check that AUTH PLAIN was called
	authChecked := false
	for i := 0; i < 10; i++ {
		select {
		case cmd := <-cmds:
			if strings.HasPrefix(cmd, "AUTH PLAIN") {
				authChecked = true
			}
		case <-time.After(500 * time.Millisecond):
			break
		}
	}

	if !authChecked {
		t.Errorf("expected AUTH PLAIN command to be sent")
	}
}

type MockDB struct {
	ExecFunc func(ctx context.Context, sql string) (interface{}, error)
}

func (m *MockDB) Execute(ctx context.Context, sql string) (interface{}, error) {
	if m.ExecFunc != nil {
		return m.ExecFunc(ctx, sql)
	}
	return nil, nil
}

func (m *MockDB) SmartQuery(ctx context.Context, sql string, vars interface{}) (interface{}, error) {
	return nil, nil
}

func (m *MockDB) Close() {}

func TestQueueNotificationThrottling(t *testing.T) {
	var executedSQL string
	mock := &MockDB{
		ExecFunc: func(ctx context.Context, sql string) (interface{}, error) {
			executedSQL = sql
			return nil, nil
		},
	}

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:                    true,
			Encryption:                 "none",
			AggregationWindow:          "1h",
			EmergencySeverityThreshold: 9,
		},
	}

	notifier := NewNotifier(cfg, mock)
	err := notifier.QueueNotification(context.Background(), "my-project", "app.log", 10, 5, "Some error log content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(executedSQL, "CREATE log_pending_notification") {
		t.Errorf("expected SQL to contain 'CREATE log_pending_notification', got %q", executedSQL)
	}
}

func TestQueueNotificationEmergencyBypass(t *testing.T) {
	l, cmds := startMockSMTPServer(t)
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	mock := &MockDB{
		ExecFunc: func(ctx context.Context, sql string) (interface{}, error) {
			t.Errorf("DB should not be called on emergency bypass")
			return nil, nil
		},
	}

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:                    true,
			Host:                       "127.0.0.1",
			Port:                       port,
			Encryption:                 "none",
			From:                       "test-sender@example.com",
			To:                         "test-receiver@example.com",
			AggregationWindow:          "1h",
			EmergencySeverityThreshold: 9,
		},
	}

	notifier := NewNotifier(cfg, mock)
	err := notifier.QueueNotification(context.Background(), "my-project", "app.log", 10, 9, "Emergency error log content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case cmd := <-cmds:
		if !strings.HasPrefix(cmd, "EHLO") {
			t.Errorf("expected EHLO command, got %s", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for immediate email send")
	}
}

func TestProcessPendingNotifications(t *testing.T) {
	l, cmds := startMockSMTPServer(t)
	defer l.Close()

	port := l.Addr().(*net.TCPAddr).Port

	var deletedQueue bool
	mock := &MockDB{
		ExecFunc: func(ctx context.Context, sql string) (interface{}, error) {
			if strings.HasPrefix(sql, "SELECT") {
				return []interface{}{
					map[string]interface{}{
						"project":      "my-project",
						"log_file":     "app.log",
						"errors_count": float64(5),
						"severity":     float64(4),
						"content":      "Log line error summary",
						"created_at":   "2026-06-11T10:00:00Z",
					},
				}, nil
			}
			if strings.HasPrefix(sql, "DELETE") {
				deletedQueue = true
				return nil, nil
			}
			return nil, nil
		},
	}

	cfg := &config.Config{
		SMTP: config.SMTPConfig{
			Enabled:                    true,
			Host:                       "127.0.0.1",
			Port:                       port,
			Encryption:                 "none",
			From:                       "test-sender@example.com",
			To:                         "test-receiver@example.com",
			AggregationWindow:          "1h",
			EmergencySeverityThreshold: 9,
		},
	}

	notifier := NewNotifier(cfg, mock)
	err := notifier.ProcessPendingNotifications(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !deletedQueue {
		t.Errorf("expected queue to be deleted after processing")
	}

	select {
	case cmd := <-cmds:
		if !strings.HasPrefix(cmd, "EHLO") {
			t.Errorf("expected EHLO command, got %s", cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for email send")
	}
}
