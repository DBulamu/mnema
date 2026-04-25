// Package email holds production adapters for the email-sender port.
//
// SMTP for prod (Resend speaks SMTP) and local (mailpit speaks SMTP) —
// the wire protocol is the same, swapping between them is a host change,
// not a code change. The Captor adapter (in captor.go) is for tests.
package email

import (
	"context"
	"fmt"
	"net/smtp"
)

// SMTPSender sends mail via plain SMTP. We deliberately use net/smtp
// rather than provider SDKs to keep the adapter vendor-neutral.
type SMTPSender struct {
	host string
	port int
	from string
}

func NewSMTPSender(host string, port int, from string) *SMTPSender {
	return &SMTPSender{host: host, port: port, from: from}
}

// Send dispatches the message. The send runs in a goroutine so we can
// honor ctx.Done() — net/smtp itself does not accept a context.
func (s *SMTPSender) Send(ctx context.Context, to, subject, text string) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	body := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.from, to, subject, text,
	)

	done := make(chan error, 1)
	go func() {
		done <- smtp.SendMail(addr, nil, s.from, []string{to}, []byte(body))
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("smtp send: %w", err)
		}
		return nil
	}
}
