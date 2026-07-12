package billing

import (
	"context"
	"log"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// emailConfig is the SMTP relay used to send transactional emails (SES SMTP works
// out of the box: EMAIL_SMTP_HOST=email-smtp.us-east-1.amazonaws.com:587 with SES
// SMTP credentials). Using SMTP avoids pulling in the AWS SDK.
type emailConfig struct {
	host string // host or host:port
	user string
	pass string
	from string
}

func loadEmailConfig() emailConfig {
	return emailConfig{
		host: os.Getenv("EMAIL_SMTP_HOST"),
		user: os.Getenv("EMAIL_SMTP_USER"),
		pass: os.Getenv("EMAIL_SMTP_PASS"),
		from: os.Getenv("EMAIL_FROM"),
	}
}

func (c emailConfig) enabled() bool { return c.host != "" && c.from != "" }

func (c emailConfig) send(to, subject, body string) error {
	addr := c.host
	hostOnly := c.host
	if i := strings.LastIndex(c.host, ":"); i >= 0 {
		hostOnly = c.host[:i]
	} else {
		addr = c.host + ":587"
	}
	msg := "From: " + c.from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" + body + "\r\n"
	var auth smtp.Auth
	if c.user != "" {
		auth = smtp.PlainAuth("", c.user, c.pass, hostOnly)
	}
	return smtp.SendMail(addr, auth, c.from, []string{to}, []byte(msg))
}

// StartEmailWorker drains the email_outbox via SMTP when a relay is configured.
// When it isn't, queued notices simply wait (still visible to admins) until one
// is set — nothing is lost.
func (s *Service) StartEmailWorker(ctx context.Context) {
	cfg := loadEmailConfig()
	if !cfg.enabled() {
		log.Println("[billing] email worker idle — EMAIL_SMTP_HOST/EMAIL_FROM unset; notices queue until configured")
		return
	}
	log.Printf("[billing] email worker started (from=%s)", cfg.from)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msgs, err := s.store.NextQueuedEmails(25)
			if err != nil {
				log.Printf("[billing] email worker: load queue failed: %v", err)
				continue
			}
			for _, m := range msgs {
				if err := cfg.send(m.To, m.Subject, m.Body); err != nil {
					log.Printf("[billing] email send to %s failed: %v", m.To, err)
					_ = s.store.MarkEmailStatus(m.ID, "failed", err.Error())
					continue
				}
				_ = s.store.MarkEmailSent(m.ID)
			}
		}
	}
}
