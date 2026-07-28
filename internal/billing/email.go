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

const (
	// maxEmailSendAttempts bounds delivery retries per queued notice before it is
	// marked failed. A transient SMTP hiccup (dropped connection, brief relay
	// throttle) shouldn't burn a notice on the first error.
	maxEmailSendAttempts = 3
	// emailRetryBackoff is the base delay between attempts (multiplied by the
	// attempt number for a small linear backoff). Kept short so the worker still
	// drains the batch promptly.
	emailRetryBackoff = 2 * time.Second
)

// sendWithRetry attempts delivery up to maxEmailSendAttempts times with a short
// linear backoff, returning the last error if all attempts fail. It honors ctx so
// a shutdown mid-backoff returns promptly rather than blocking the worker.
func sendWithRetry(ctx context.Context, cfg emailConfig, m QueuedEmail) error {
	var err error
	for attempt := 1; attempt <= maxEmailSendAttempts; attempt++ {
		if err = cfg.send(m.To, m.Subject, m.Body); err == nil {
			return nil
		}
		if attempt < maxEmailSendAttempts {
			log.Printf("[billing] email send to %s attempt %d/%d failed: %v (retrying)", m.To, attempt, maxEmailSendAttempts, err)
			select {
			case <-ctx.Done():
				return err
			case <-time.After(emailRetryBackoff * time.Duration(attempt)):
			}
		}
	}
	return err
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
				if err := sendWithRetry(ctx, cfg, m); err != nil {
					log.Printf("[billing] email send to %s failed after %d attempts: %v", m.To, maxEmailSendAttempts, err)
					_ = s.store.MarkEmailStatus(m.ID, "failed", err.Error())
					continue
				}
				_ = s.store.MarkEmailSent(m.ID)
			}
		}
	}
}
