package logs

import (
	"fmt"
	"net/smtp"
	"os"

	"github.com/terenzif/ibis-arc/internal/config"
	"github.com/terenzif/ibis-arc/internal/logger"
)

type Notifier struct {
	Cfg *config.Config
}

func NewNotifier(cfg *config.Config) *Notifier {
	return &Notifier{Cfg: cfg}
}

func (n *Notifier) SendEmail(reportPath string) error {
	if !n.Cfg.SMTP.Enabled {
		return nil
	}

	content, err := os.ReadFile(reportPath)
	if err != nil {
		return err
	}

	auth := smtp.PlainAuth("", n.Cfg.SMTP.User, n.Cfg.SMTP.Password, n.Cfg.SMTP.Host)

	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"From: %s\r\n"+
		"Subject: Ibis Arc - Log Report\r\n"+
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n"+
		"\r\n"+
		"%s\r\n", n.Cfg.SMTP.To, n.Cfg.SMTP.From, string(content)))

	addr := fmt.Sprintf("%s:%d", n.Cfg.SMTP.Host, n.Cfg.SMTP.Port)
	err = smtp.SendMail(addr, auth, n.Cfg.SMTP.From, []string{n.Cfg.SMTP.To}, msg)
	if err != nil {
		logger.Error("Failed to send email to %s: %v", n.Cfg.SMTP.To, err)
		return err
	}

	logger.Info("Report email sent successfully to %s", n.Cfg.SMTP.To)
	return nil
}
