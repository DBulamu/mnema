// Package email wraps outbound transactional email behind a Sender interface.
//
// One interface, three concrete implementations chosen by APP_ENV:
//   - local: SMTP against mailpit (captures the email locally, never sends)
//   - test:  in-memory Captor, so tests can assert on the message contents
//   - prod:  SMTP against Resend
//
// We use plain net/smtp instead of an SDK because the wire protocol is the
// same — switching providers is a config change, not a code change. This is
// the same vendor-agnostic pattern we apply to the LLM layer (H20).
package email

import (
	"context"
	"fmt"
	"net/smtp"
	"sync"

	"github.com/DBulamu/mnema/backend/internal/config"
)

type Message struct {
	To      string
	Subject string
	Text    string
}

type Sender interface {
	Send(ctx context.Context, msg Message) error
}

func New(cfg config.Config) Sender {
	switch cfg.Env {
	case config.EnvTest:
		return NewCaptor()
	default:
		return &smtpSender{
			host: cfg.SMTP.Host,
			port: cfg.SMTP.Port,
			from: cfg.SMTP.From,
		}
	}
}

type smtpSender struct {
	host string
	port int
	from string
}

func (s *smtpSender) Send(ctx context.Context, msg Message) error {
	addr := fmt.Sprintf("%s:%d", s.host, s.port)
	body := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.from, msg.To, msg.Subject, msg.Text,
	)

	done := make(chan error, 1)
	go func() {
		done <- smtp.SendMail(addr, nil, s.from, []string{msg.To}, []byte(body))
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

type Captor struct {
	mu       sync.Mutex
	Messages []Message
}

func NewCaptor() *Captor {
	return &Captor{}
}

func (c *Captor) Send(_ context.Context, msg Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Messages = append(c.Messages, msg)
	return nil
}

func (c *Captor) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Messages = nil
}
