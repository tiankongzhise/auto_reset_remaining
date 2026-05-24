package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"sync"

	"auto_reset_remaining/internal/config"
)

type Sender interface {
	Send(ctx context.Context, subject string, body string) error
}

type SMTPMailer struct {
	mu  sync.RWMutex
	cfg config.SMTPConfig
}

func NewSMTPMailer(cfg config.SMTPConfig) *SMTPMailer {
	return &SMTPMailer{cfg: cloneSMTPConfig(cfg)}
}

func (m *SMTPMailer) SetConfig(cfg config.SMTPConfig) {
	m.mu.Lock()
	m.cfg = cloneSMTPConfig(cfg)
	m.mu.Unlock()
}

func (m *SMTPMailer) Send(ctx context.Context, subject string, body string) error {
	cfg := m.snapshot()
	if len(cfg.To) == 0 {
		return fmt.Errorf("SMTP_TO is empty")
	}
	message := message(cfg, subject, body)
	address := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	dialer := net.Dialer{}

	var conn net.Conn
	var err error
	if cfg.Port == 465 {
		conn, err = tls.DialWithDialer(&dialer, "tcp", address, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer client.Close()

	if cfg.Port != 465 {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		}
	}

	if cfg.User != "" {
		auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return err
	}
	for _, recipient := range cfg.To {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	_ = client.Quit()
	return nil
}

func (m *SMTPMailer) snapshot() config.SMTPConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneSMTPConfig(m.cfg)
}

func cloneSMTPConfig(cfg config.SMTPConfig) config.SMTPConfig {
	cfg.To = append([]string(nil), cfg.To...)
	return cfg
}

func message(cfg config.SMTPConfig, subject string, body string) []byte {
	headers := []string{
		"From: " + cfg.From,
		"To: " + strings.Join(cfg.To, ", "),
		"Subject: " + mime.BEncoding.Encode("UTF-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
	}
	return []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n")
}
