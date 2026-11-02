package mail

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server without STARTTLS or AUTH that records
// the envelope and data of every message it receives.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	from, to []string
	data     []string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("220 fake ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch cmd {
		case "EHLO", "HELO":
			_ = tp.PrintfLine("250 fake")
		case "MAIL":
			f.mu.Lock()
			f.from = append(f.from, line)
			f.mu.Unlock()
			_ = tp.PrintfLine("250 ok")
		case "RCPT":
			f.mu.Lock()
			f.to = append(f.to, line)
			f.mu.Unlock()
			_ = tp.PrintfLine("250 ok")
		case "DATA":
			_ = tp.PrintfLine("354 go ahead")
			body, err := io.ReadAll(tp.DotReader())
			if err != nil {
				return
			}
			f.mu.Lock()
			f.data = append(f.data, string(body))
			f.mu.Unlock()
			_ = tp.PrintfLine("250 queued")
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("502 not implemented")
		}
	}
}

func TestSMTPSend(t *testing.T) {
	f := startFakeSMTP(t)
	host, portStr, _ := net.SplitHostPort(f.ln.Addr().String())
	port, _ := strconv.Atoi(portStr)

	m := NewSMTP(SMTPConfig{Host: host, Port: port, From: "CRM <no-reply@crm.test>"})
	err := m.Send(context.Background(), Message{
		To:      "Asha <asha@example.com>",
		Subject: "Reset your password",
		Text:    "Open this link:\nhttps://app.test/reset-password?token=abc_DEF-123\n",
	})
	if err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.data) != 1 {
		t.Fatalf("messages = %d, want 1", len(f.data))
	}
	if f.from[0] != "MAIL FROM:<no-reply@crm.test>" || !strings.HasPrefix(f.to[0], "RCPT TO:<asha@example.com>") {
		t.Errorf("envelope = %q / %q", f.from[0], f.to[0])
	}
	msg := f.data[0]
	for _, want := range []string{
		"From: \"CRM\" <no-reply@crm.test>",
		"To: \"Asha\" <asha@example.com>",
		"Subject: Reset your password",
		"Content-Type: text/plain; charset=UTF-8",
		"https://app.test/reset-password?token=3Dabc_DEF-123", // quoted-printable "="
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestSMTPCredentialsNeedTLSUnlessLocal(t *testing.T) {
	// The fake server offers no STARTTLS. Against loopback, auth is attempted
	// (and fails because the fake has no AUTH); credentials are never sent
	// in clear to a remote host, which isLocalHost decides.
	f := startFakeSMTP(t)
	host, portStr, _ := net.SplitHostPort(f.ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	m := NewSMTP(SMTPConfig{Host: host, Port: port, User: "u", Pass: "p", From: "a@crm.test"})
	if err := m.Send(context.Background(), Message{To: "b@crm.test", Subject: "x", Text: "y"}); err == nil || !strings.Contains(err.Error(), "auth") {
		t.Errorf("loopback without AUTH support: err = %v, want auth error", err)
	}

	for host, want := range map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true,
		"smtp.example.com": false, "10.0.0.5": false,
	} {
		if got := isLocalHost(host); got != want {
			t.Errorf("isLocalHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestBuildMessageRejectsHeaderInjection(t *testing.T) {
	m := NewSMTP(SMTPConfig{Host: "127.0.0.1", Port: 1, From: "a@crm.test"})
	err := m.Send(context.Background(), Message{To: "b@crm.test", Subject: "hi\r\nBcc: evil@example.com", Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "line breaks") {
		t.Errorf("err = %v, want line-break rejection", err)
	}
}

type recordingMailer struct {
	mu   sync.Mutex
	msgs []Message
}

func (r *recordingMailer) Send(_ context.Context, msg Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
	return nil
}

func TestAsyncWait(t *testing.T) {
	rec := &recordingMailer{}
	a := NewAsync(rec, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	_ = a.Send(ctx, Message{To: "a@b.c", Subject: "s", Text: "t"})
	cancel() // a finished request must not cancel the background send
	a.Wait()
	if len(rec.msgs) != 1 {
		t.Errorf("sent %d messages, want 1", len(rec.msgs))
	}
}
