package mixnet

import (
	"bytes"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/mixnet/ces"
)

func TestDestinationHandlerReconstructsAtThreshold(t *testing.T) {
	t.Parallel()

	pipeline := ces.NewPipeline(&ces.Config{
		HopCount:         2,
		CircuitCount:     4,
		Compression:      "gzip",
		ErasureThreshold: 2,
	})

	original := bytes.Repeat([]byte("mixnet-threshold-check-"), 256)
	compressed, err := pipeline.Compressor().Compress(original)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	encrypted, keyData, err := encryptSessionPayload(compressed)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	shards, err := pipeline.Sharder().Shard(encrypted)
	if err != nil {
		t.Fatalf("shard: %v", err)
	}

	handler := &DestinationHandler{
		pipeline:    pipeline,
		shardBuf:    make(map[string][]*ces.Shard),
		shardTags:   make(map[string]map[int][]byte),
		totalShards: make(map[string]int),
		timers:      make(map[string]*time.Timer),
		sessions:    make(map[string]chan []byte),
		keys:        make(map[string]sessionKey),
		keyData:     make(map[string][]byte),
		inboundCh:   make(chan string, 1),
		threshold:   2,
		timeout:     5 * time.Second,
		dataCh:      make(chan []byte, 1),
		stopCh:      make(chan struct{}),
	}

	const sessionID = "threshold-session"
	handler.AddShard(sessionID, shards[1], keyData, nil, len(shards))
	if _, err := handler.TryReconstruct(sessionID); err == nil {
		t.Fatal("expected reconstruction to wait for the threshold")
	}

	handler.AddShard(sessionID, shards[3], keyData, nil, len(shards))
	reconstructed, err := handler.TryReconstruct(sessionID)
	if err != nil {
		t.Fatalf("reconstruct at threshold: %v", err)
	}
	if !bytes.Equal(reconstructed, original) {
		t.Fatal("reconstructed payload mismatch")
	}
}

func TestSingleShardSharderRoundTrips(t *testing.T) {
	t.Parallel()

	sharder := ces.NewSharder(1, 1)
	payload := bytes.Repeat([]byte("single-shard-ces-"), 64)

	shards, err := sharder.Shard(payload)
	if err != nil {
		t.Fatalf("shard: %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(shards))
	}

	out, err := sharder.Reconstruct(shards)
	if err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Fatal("single-shard round-trip mismatch")
	}
}
