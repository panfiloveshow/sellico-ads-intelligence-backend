// Package email provides SMTP delivery for report notifications.
package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Config struct {
	Host      string
	Port      int
	Username  string
	Password  string
	FromEmail string
	FromName  string
	Timeout   time.Duration
}

type Client struct {
	cfg Config
}

func NewClient(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return &Client{cfg: cfg}
}

func (c *Client) IsConfigured() bool {
	return c != nil && strings.TrimSpace(c.cfg.Host) != "" && c.cfg.Port > 0 && strings.TrimSpace(c.cfg.FromEmail) != ""
}

func (c *Client) SendPlainText(ctx context.Context, recipients []string, subject, body string) error {
	if !c.IsConfigured() {
		return fmt.Errorf("smtp email sender is not configured")
	}
	cleanRecipients, err := normalizeRecipients(recipients)
	if err != nil {
		return err
	}
	if len(cleanRecipients) == 0 {
		return fmt.Errorf("email recipients are empty")
	}

	from := mail.Address{Name: c.cfg.FromName, Address: strings.TrimSpace(c.cfg.FromEmail)}
	message := buildPlainTextMessage(from.String(), cleanRecipients, subject, body)

	addr := fmt.Sprintf("%s:%d", strings.TrimSpace(c.cfg.Host), c.cfg.Port)
	dialer := net.Dialer{Timeout: c.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, strings.TrimSpace(c.cfg.Host))
	if err != nil {
		return fmt.Errorf("create smtp client: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: strings.TrimSpace(c.cfg.Host), MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("start tls: %w", err)
		}
	}

	if strings.TrimSpace(c.cfg.Username) != "" {
		auth := smtp.PlainAuth("", c.cfg.Username, c.cfg.Password, strings.TrimSpace(c.cfg.Host))
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, recipient := range cleanRecipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp recipient %s: %w", recipient, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write smtp message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close smtp message: %w", err)
	}
	return client.Quit()
}

func normalizeRecipients(recipients []string) ([]string, error) {
	result := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		address, err := mail.ParseAddress(recipient)
		if err != nil || address.Address != recipient {
			return nil, fmt.Errorf("invalid email recipient %q", recipient)
		}
		result = append(result, recipient)
	}
	return result, nil
}

func buildPlainTextMessage(from string, recipients []string, subject, body string) string {
	headers := []string{
		"From: " + from,
		"To: " + strings.Join(recipients, ", "),
		"Subject: " + sanitizeHeader(subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body
}

func sanitizeHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}
