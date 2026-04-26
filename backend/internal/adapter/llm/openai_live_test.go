//go:build openai_live

// These tests hit the real OpenAI chat-completions endpoint and incur
// cost. They are gated behind both a build tag and the
// MNEMA_OPENAI_KEY environment variable so they cannot run by accident
// in CI or on a dev machine without the operator opting in.
//
// Run with:
//   MNEMA_OPENAI_KEY=sk-... go test -tags=openai_live -v -run TestLive ./internal/adapter/llm/

package llm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func liveKey(t *testing.T) string {
	t.Helper()
	k := os.Getenv("MNEMA_OPENAI_KEY")
	if k == "" {
		t.Skip("MNEMA_OPENAI_KEY not set — skipping live OpenAI test")
	}
	return k
}

// TestLiveOpenAIReply_GPT4oMini verifies a real call against
// gpt-4o-mini returns non-empty Russian text. We don't assert on the
// content (the model may vary), only that the wire path works
// end-to-end with auth, system prompt, and max_tokens cap.
func TestLiveOpenAIReply_GPT4oMini(t *testing.T) {
	key := liveKey(t)

	client, err := NewOpenAI(key, "gpt-4o-mini",
		WithOpenAISystemPrompt("Отвечай кратко по-русски, одним предложением."),
		WithOpenAIMaxTokens(64),
	)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reply, err := client.Reply(ctx, []Turn{
		{Role: "user", Content: "Скажи привет."},
	})
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatal("empty reply from live model")
	}
	t.Logf("live reply: %q", reply)
}

// TestLiveOpenAIReplyStream_GPT4oMini exercises the SSE path against
// the real endpoint: deltas must arrive in order, and the assembled
// answer must equal the concatenation of deltas.
func TestLiveOpenAIReplyStream_GPT4oMini(t *testing.T) {
	key := liveKey(t)

	client, err := NewOpenAI(key, "gpt-4o-mini",
		WithOpenAISystemPrompt("Отвечай кратко по-русски, одним предложением."),
		WithOpenAIMaxTokens(64),
	)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var assembled strings.Builder
	deltaCount := 0
	full, err := client.ReplyStream(ctx, []Turn{
		{Role: "user", Content: "Скажи привет."},
	}, func(piece string) error {
		deltaCount++
		assembled.WriteString(piece)
		return nil
	})
	if err != nil {
		t.Fatalf("ReplyStream: %v", err)
	}
	if deltaCount < 2 {
		// 1 delta would mean the API returned the whole message in
		// one frame — possible for very short replies, but with a
		// 64-token cap we should still see multiple chunks.
		t.Logf("warning: only %d deltas — usually OK but rare for >5 token replies", deltaCount)
	}
	if assembled.String() == "" {
		t.Fatal("no deltas accumulated")
	}
	if strings.TrimSpace(full) != strings.TrimSpace(assembled.String()) {
		t.Errorf("assembled stream %q != final %q", assembled.String(), full)
	}
	t.Logf("live stream final: %q (%d deltas)", full, deltaCount)
}
