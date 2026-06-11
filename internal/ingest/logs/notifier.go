package logs

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/terenzif/ibis-arc/internal/config"
	"github.com/terenzif/ibis-arc/internal/db"
	"github.com/terenzif/ibis-arc/internal/logger"
	"github.com/terenzif/ibis-arc/internal/schema"
)

type Notifier struct {
	Cfg *config.Config
	DB  db.Executor
}

func NewNotifier(cfg *config.Config, dbClient db.Executor) *Notifier {
	return &Notifier{Cfg: cfg, DB: dbClient}
}

func (n *Notifier) SendEmail(reportPath string) error {
	if !n.Cfg.SMTP.Enabled {
		return nil
	}

	content, err := os.ReadFile(reportPath)
	if err != nil {
		return err
	}

	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s\r\n"+
		"Subject: Ibis Arc - Log Report\r\n"+
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n"+
		"\r\n"+
		"%s\r\n", n.Cfg.SMTP.To, n.Cfg.SMTP.From, string(content)))

	err = n.SendRawEmail(msg)
	if err != nil {
		logger.Error("Failed to send email to %s: %v", n.Cfg.SMTP.To, err)
		return err
	}

	logger.Info("Report email sent successfully to %s", n.Cfg.SMTP.To)
	return nil
}

func (n *Notifier) QueueNotification(ctx context.Context, project, logFile string, errorsCount int, maxSeverity int, content string) error {
	if !n.Cfg.SMTP.Enabled {
		return nil
	}

	// Bypass aggregation if severity is >= threshold (Emergency!)
	if maxSeverity >= n.Cfg.SMTP.EmergencySeverityThreshold {
		logger.Warn("Emergency severity threshold reached (%d >= %d). Bypassing aggregation.", maxSeverity, n.Cfg.SMTP.EmergencySeverityThreshold)
		subject := fmt.Sprintf("CRITICAL ALERT: %s - Emergency Log Report", project)
		msg := []byte(fmt.Sprintf("To: %s\r\n"+
			"From: %s\r\n"+
			"Subject: %s\r\n"+
			"Content-Type: text/plain; charset=\"UTF-8\"\r\n"+
			"\r\n"+
			"Emergency Alert: Critical errors detected in project %s (file: %s).\r\n"+
			"Errors Count: %d\r\n"+
			"Severity: %d\r\n\r\n"+
			"Content:\r\n%s\r\n", n.Cfg.SMTP.To, n.Cfg.SMTP.From, subject, project, logFile, errorsCount, maxSeverity, content))
		return n.SendRawEmail(msg)
	}

	// Send immediately if no aggregation window is configured
	if n.Cfg.SMTP.AggregationWindow == "" || n.Cfg.SMTP.AggregationWindow == "none" {
		subject := fmt.Sprintf("Ibis Arc: %s - Log Report", project)
		msg := []byte(fmt.Sprintf("To: %s\r\n"+
			"From: %s\r\n"+
			"Subject: %s\r\n"+
			"Content-Type: text/plain; charset=\"UTF-8\"\r\n"+
			"\r\n"+
			"Errors detected in project %s (file: %s).\r\n"+
			"Errors Count: %d\r\n"+
			"Severity: %d\r\n\r\n"+
			"Content:\r\n%s\r\n", n.Cfg.SMTP.To, n.Cfg.SMTP.From, subject, project, logFile, errorsCount, maxSeverity, content))
		return n.SendRawEmail(msg)
	}

	if n.DB == nil {
		logger.Warn("DB is nil, sending log report immediately.")
		subject := fmt.Sprintf("Ibis Arc: %s - Log Report (No DB Fallback)", project)
		msg := []byte(fmt.Sprintf("To: %s\r\n"+
			"From: %s\r\n"+
			"Subject: %s\r\n"+
			"Content-Type: text/plain; charset=\"UTF-8\"\r\n"+
			"\r\n"+
			"%s\r\n", n.Cfg.SMTP.To, n.Cfg.SMTP.From, subject, content))
		return n.SendRawEmail(msg)
	}

	timestamp := time.Now().Format(time.RFC3339)
	query := fmt.Sprintf("CREATE %s SET project = '%s', log_file = '%s', errors_count = %d, severity = %d, content = '%s', created_at = '%s';",
		schema.TableLogPendingNotification, db.EscapeSQL(project), db.EscapeSQL(logFile), errorsCount, maxSeverity, db.EscapeSQL(content), timestamp)
	_, err := n.DB.Execute(ctx, query)
	if err != nil {
		logger.Error("Failed to queue pending notification: %v", err)
		return err
	}

	logger.Info("Notification queued for aggregation (%s window)", n.Cfg.SMTP.AggregationWindow)
	return nil
}

func (n *Notifier) ProcessPendingNotifications(ctx context.Context) error {
	if n.DB == nil || !n.Cfg.SMTP.Enabled {
		return nil
	}

	query := fmt.Sprintf("SELECT * FROM %s ORDER BY created_at ASC;", schema.TableLogPendingNotification)
	res, err := n.DB.Execute(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to fetch pending notifications: %w", err)
	}

	rows, ok := res.([]interface{})
	if !ok || len(rows) == 0 {
		return nil // Nothing to send
	}

	var sb strings.Builder
	sb.WriteString("# Ibis Arc - Aggregated Log Digest\r\n\r\n")
	sb.WriteString(fmt.Sprintf("Report Date: %s\r\n\r\n", time.Now().Format(time.RFC822)))
	sb.WriteString("The following errors were captured and aggregated during the monitoring window:\r\n\r\n")

	for _, r := range rows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		proj, _ := row["project"].(string)
		logFile, _ := row["log_file"].(string)
		errsCount, _ := row["errors_count"].(float64)
		severity, _ := row["severity"].(float64)
		content, _ := row["content"].(string)
		createdAt, _ := row["created_at"].(string)

		sb.WriteString(fmt.Sprintf("### Project: %s (File: %s)\r\n", proj, logFile))
		sb.WriteString(fmt.Sprintf("- **Time**: %s\r\n", createdAt))
		sb.WriteString(fmt.Sprintf("- **Errors Count**: %d\r\n", int(errsCount)))
		sb.WriteString(fmt.Sprintf("- **Max Severity**: %d/10\r\n", int(severity)))
		sb.WriteString("- **Details Snippet**:\r\n")
		sb.WriteString("```\r\n")
		if len(content) > 500 {
			sb.WriteString(content[:500] + "\r\n... [TRUNCATED] ...\r\n")
		} else {
			sb.WriteString(content + "\r\n")
		}
		sb.WriteString("```\r\n\r\n")
	}

	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s\r\n"+
		"Subject: Ibis Arc - Log Digest (%d errors)\r\n"+
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n"+
		"\r\n"+
		"%s\r\n", n.Cfg.SMTP.To, n.Cfg.SMTP.From, len(rows), sb.String()))

	err = n.SendRawEmail(msg)
	if err != nil {
		return fmt.Errorf("failed to send digest email: %w", err)
	}

	_, err = n.DB.Execute(ctx, fmt.Sprintf("DELETE %s;", schema.TableLogPendingNotification))
	if err != nil {
		logger.Error("Failed to clear pending notifications queue: %v", err)
	}

	logger.Info("Sent digest email containing %d events and cleared queue", len(rows))
	return nil
}

func (n *Notifier) SendRawEmail(msg []byte) error {
	addr := fmt.Sprintf("%s:%d", n.Cfg.SMTP.Host, n.Cfg.SMTP.Port)
	var c *smtp.Client
	var err error

	encryption := strings.ToLower(n.Cfg.SMTP.Encryption)

	if encryption == "ssl_tls" {
		tlsConfig := &tls.Config{
			ServerName: n.Cfg.SMTP.Host,
		}
		conn, err := tls.Dial("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to connect via SSL/TLS: %w", err)
		}
		c, err = smtp.NewClient(conn, n.Cfg.SMTP.Host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("failed to create SMTP client: %w", err)
		}
	} else {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}
		c, err = smtp.NewClient(conn, n.Cfg.SMTP.Host)
		if err != nil {
			conn.Close()
			return fmt.Errorf("failed to create SMTP client: %w", err)
		}

		if encryption == "starttls" {
			tlsConfig := &tls.Config{
				ServerName: n.Cfg.SMTP.Host,
			}
			if err = c.StartTLS(tlsConfig); err != nil {
				c.Close()
				return fmt.Errorf("failed to start TLS: %w", err)
			}
		}
	}
	defer c.Close()

	if n.Cfg.SMTP.User != "" && n.Cfg.SMTP.Password != "" {
		auth := smtp.PlainAuth("", n.Cfg.SMTP.User, n.Cfg.SMTP.Password, n.Cfg.SMTP.Host)
		if err = c.Auth(auth); err != nil {
			return fmt.Errorf("failed to authenticate: %w", err)
		}
	}

	if err = c.Mail(n.Cfg.SMTP.From); err != nil {
		return fmt.Errorf("failed to set MAIL FROM: %w", err)
	}
	if err = c.Rcpt(n.Cfg.SMTP.To); err != nil {
		return fmt.Errorf("failed to set RCPT TO: %w", err)
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("failed to open DATA writer: %w", err)
	}
	_, err = w.Write(msg)
	if err != nil {
		w.Close()
		return fmt.Errorf("failed to write message: %w", err)
	}
	err = w.Close()
	if err != nil {
		return fmt.Errorf("failed to close DATA writer: %w", err)
	}

	return c.Quit()
}
