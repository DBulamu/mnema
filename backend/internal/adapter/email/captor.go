package email

import (
	"context"
	"sync"
)

// CapturedMessage is a record of an email the Captor saw — used in tests
// to assert on rendered output.
type CapturedMessage struct {
	To      string
	Subject string
	Text    string
}

// Captor records messages in memory. Used in test environments so the
// auth flow can be asserted without an SMTP round-trip.
type Captor struct {
	mu       sync.Mutex
	messages []CapturedMessage
}

func NewCaptor() *Captor { return &Captor{} }

func (c *Captor) Send(_ context.Context, to, subject, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, CapturedMessage{To: to, Subject: subject, Text: text})
	return nil
}

// Snapshot returns a copy of the captured messages — callers should not
// hold the slice across further sends.
func (c *Captor) Snapshot() []CapturedMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CapturedMessage, len(c.messages))
	copy(out, c.messages)
	return out
}

func (c *Captor) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = nil
}
