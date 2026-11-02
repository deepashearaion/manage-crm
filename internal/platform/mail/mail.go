// Package mail sends transactional email. Mailer is the interface modules
// depend on; SMTP is the production implementation, built on net/smtp.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Message is a plain-text email.
type Message struct {
	To      string
	Subject string
	Text    string
}

type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// SMTPConfig configures the SMTP mailer.
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

// SMTP sends mail through an SMTP server. Port 465 uses implicit TLS; other
// ports upgrade with STARTTLS when the server offers it. Credentials are never
// sent over an unencrypted connection to a non-local server.
type SMTP struct {
	cfg SMTPConfig
}

func NewSMTP(cfg SMTPConfig) *SMTP {
	return &SMTP{cfg: cfg}
}

func (s *SMTP) Send(ctx context.Context, msg Message) error {
	from, err := mail.ParseAddress(s.cfg.From)
	if err != nil {
		return fmt.Errorf("invalid from address: %w", err)
	}
	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("invalid recipient address: %w", err)
	}
	data, err := buildMessage(from, to, msg.Subject, msg.Text, time.Now())
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	tlsConfig := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	var conn net.Conn
	if s.cfg.Port == 465 {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP handshake: %w", err)
	}
	defer c.Close()

	if s.cfg.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("STARTTLS: %w", err)
			}
		} else if s.cfg.User != "" && !isLocalHost(s.cfg.Host) {
			return errors.New("SMTP server does not offer STARTTLS; refusing to send credentials unencrypted")
		}
	}
	if s.cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, s.cfg.Host)); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("SMTP MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return fmt.Errorf("SMTP RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("SMTP write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP end of data: %w", err)
	}
	return c.Quit()
}

func isLocalHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func buildMessage(from, to *mail.Address, subject, text string, now time.Time) ([]byte, error) {
	if strings.ContainsAny(subject, "\r\n") {
		return nil, errors.New("subject must not contain line breaks")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]

	var buf bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	header("From", from.String())
	header("To", to.String())
	header("Subject", mime.QEncoding.Encode("utf-8", subject))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<%s@%s>", hex.EncodeToString(id), domain))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=UTF-8")
	header("Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(strings.ReplaceAll(text, "\n", "\r\n"))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Log "sends" mail by logging it. Only for local development without an
// SMTP server: the message body (which may hold reset links) is logged.
type Log struct {
	Logger *slog.Logger
}

func (l Log) Send(ctx context.Context, msg Message) error {
	l.Logger.InfoContext(ctx, "email (not sent: SMTP not configured)",
		slog.String("to", msg.To), slog.String("subject", msg.Subject), slog.String("body", msg.Text))
	return nil
}

// Async sends mail in the background so request latency does not depend on
// the mail server (or reveal whether an email was sent at all). Failures are
// logged. Call Wait during shutdown to let pending sends finish.
type Async struct {
	next    Mailer
	logger  *slog.Logger
	timeout time.Duration
	wg      sync.WaitGroup
}

func NewAsync(next Mailer, logger *slog.Logger, timeout time.Duration) *Async {
	return &Async{next: next, logger: logger, timeout: timeout}
}

func (a *Async) Send(ctx context.Context, msg Message) error {
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.timeout)
		defer cancel()
		if err := a.next.Send(ctx, msg); err != nil {
			a.logger.ErrorContext(ctx, "send email failed", slog.String("subject", msg.Subject), slog.Any("error", err))
		}
	}()
	return nil
}

func (a *Async) Wait() { a.wg.Wait() }
