package llm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"strings"
)

// embeddingStubModel is the model identifier the stub reports. Picked
// distinct from any real OpenAI name so a value left in the database
// after a stubbed run is obvious in logs and easy to grep for during a
// re-embed.
const embeddingStubModel = "stub-sha256-d8"

// embeddingStubDim is the vector size produced by the stub. The
// production OpenAI model (text-embedding-3-small) is 1536-dimensional;
// we mirror that here so DB inserts hit the same VECTOR(1536) column
// without needing a separate test schema.
const embeddingStubDim = 1536

// EmbedderStub is the deterministic embedder wired in local/test. It
// hashes the input with SHA-256 and tiles the digest into a 1536-dim
// vector with values in [-1, 1). The result is:
//   - deterministic (same text → same vector across runs);
//   - distinct across non-trivially-different inputs (good enough for
//     "did embedding actually run" assertions);
//   - free of any network dependency.
//
// It is NOT semantically meaningful — cosine similarity between two
// stub vectors says nothing about whether the texts mean similar things.
// That is fine for plumbing tests; semantic search is gated on the real
// OpenAI provider being wired up.
type EmbedderStub struct{}

func NewEmbedderStub() *EmbedderStub { return &EmbedderStub{} }

// Model returns the stub identifier so the persistence layer can store
// it next to the vector. A future re-embed pass keys off this string.
func (e *EmbedderStub) Model() string { return embeddingStubModel }

// Embed hashes the trimmed text and tiles the 32-byte digest into a
// 1536-dim float32 vector. Empty input yields a nil vector — the
// usecase treats that as a no-op embedding and counts it as a failure
// rather than writing a zero-vector that would skew similarity search.
func (e *EmbedderStub) Embed(_ context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	digest := sha256.Sum256([]byte(text))
	out := make([]float32, embeddingStubDim)
	// Tile the 8 uint32s of the digest across the vector. Each uint32
	// becomes a float32 in [-1, 1) by dividing by 2^31. Different texts
	// land on different tile values; identical texts land on identical
	// vectors, which is the property the stub is meant to provide.
	for i := 0; i < embeddingStubDim; i++ {
		group := (i / (embeddingStubDim / 8)) * 4
		u := binary.BigEndian.Uint32(digest[group : group+4])
		out[i] = float32(int32(u)) / float32(1<<31)
	}
	return out, nil
}
