package ces

import (
	"bytes"
	"testing"
)

func TestCESPipelineRoundTrip(t *testing.T) {
	cfg := &Config{
		HopCount:         2,
		CircuitCount:     4,
		Compression:      "snappy",
		ErasureThreshold: 2,
	}
	pipeline := NewPipeline(cfg)

	data := bytes.Repeat([]byte("mixnet-buffer-pool-"), 64)
	destinations := []string{"peer-a", "peer-b"}

	shards, keys, err := pipeline.ProcessWithKeys(data, destinations)
	if err != nil {
		t.Fatalf("ProcessWithKeys() error = %v", err)
	}
	if len(shards) < 2 {
		t.Fatalf("expected at least 2 shards, got %d", len(shards))
	}

	reconstructed, err := pipeline.Reconstruct(shards[:2], keys)
	if err != nil {
		t.Fatalf("Reconstruct() error = %v", err)
	}
	if !bytes.Equal(reconstructed, data) {
		t.Fatalf("round trip mismatch")
	}
}

func TestSharderReconstructHandlesSplitLengthPrefix(t *testing.T) {
	sharder := NewSharder(10, 9)
	payload := []byte("x")

	shards, err := sharder.Shard(payload)
	if err != nil {
		t.Fatalf("Shard() error = %v", err)
	}
	if len(shards) < 9 {
		t.Fatalf("expected at least 9 shards, got %d", len(shards))
	}

	reconstructed, err := sharder.Reconstruct(shards[:9])
	if err != nil {
		t.Fatalf("Reconstruct() error = %v", err)
	}
	if !bytes.Equal(reconstructed, payload) {
		t.Fatalf("reconstructed payload = %q, want %q", reconstructed, payload)
	}
}
