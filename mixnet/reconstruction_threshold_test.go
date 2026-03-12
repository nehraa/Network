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
		shardBuf:    make(map[string]map[int]*ces.Shard),
		shardTags:   make(map[string]map[int][]byte),
		totalShards: make(map[string]int),
		timers:      make(map[string]*time.Timer),
		sessions:    make(map[string]*sessionMailbox),
		sessionDone: make(map[string]time.Time),
		keys:        make(map[string]sessionKey),
		keyData:     make(map[string][]byte),
		inboundCh:   make(chan string, 1),
		threshold:   2,
		timeout:     5 * time.Second,
		dataCh:      make(chan []byte, 1),
		stopCh:      make(chan struct{}),
	}

	const sessionID = "threshold-session"
	if err := handler.AddShard(sessionID, shards[1], keyData, nil, len(shards)); err != nil {
		t.Fatalf("add shard 1: %v", err)
	}
	if _, err := handler.TryReconstruct(sessionID); err == nil {
		t.Fatal("expected reconstruction to wait for the threshold")
	}

	if err := handler.AddShard(sessionID, shards[3], keyData, nil, len(shards)); err != nil {
		t.Fatalf("add shard 3: %v", err)
	}
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

func TestDestinationHandlerReconstructsNonCESPerShardEncryption(t *testing.T) {
	t.Parallel()

	original := bytes.Repeat([]byte("mixnet-nonces-hard-stop-"), 256)
	shards, keyData, err := encryptSessionShards(original, 3)
	if err != nil {
		t.Fatalf("encrypt shards: %v", err)
	}

	handler := &DestinationHandler{
		pipeline:    nil,
		shardBuf:    make(map[string]map[int]*ces.Shard),
		shardTags:   make(map[string]map[int][]byte),
		totalShards: make(map[string]int),
		timers:      make(map[string]*time.Timer),
		sessions:    make(map[string]*sessionMailbox),
		sessionDone: make(map[string]time.Time),
		keys:        make(map[string]sessionKey),
		keyData:     make(map[string][]byte),
		inboundCh:   make(chan string, 1),
		threshold:   3,
		timeout:     5 * time.Second,
		dataCh:      make(chan []byte, 1),
		stopCh:      make(chan struct{}),
	}

	const sessionID = "non-ces-shard-session"
	order := []int{2, 0, 1}
	for _, idx := range order {
		if err := handler.AddShard(sessionID, shards[idx], keyData, nil, len(shards)); err != nil {
			t.Fatalf("add shard %d: %v", idx, err)
		}
	}

	reconstructed, err := handler.TryReconstruct(sessionID)
	if err != nil {
		t.Fatalf("reconstruct non-CES shards: %v", err)
	}
	if !bytes.Equal(reconstructed, original) {
		t.Fatal("non-CES reconstructed payload mismatch")
	}
}

func TestDestinationHandlerIgnoresLateShardAfterThreshold(t *testing.T) {
	t.Parallel()

	pipeline := ces.NewPipeline(&ces.Config{
		HopCount:         2,
		CircuitCount:     3,
		Compression:      "gzip",
		ErasureThreshold: 2,
	})

	original := bytes.Repeat([]byte("late-threshold-shard-check-"), 128)
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
		pipeline:       pipeline,
		shardBuf:       make(map[string]map[int]*ces.Shard),
		shardTags:      make(map[string]map[int][]byte),
		totalShards:    make(map[string]int),
		timers:         make(map[string]*time.Timer),
		sessions:       make(map[string]*sessionMailbox),
		sessionPending: make(map[string]map[uint64][]byte),
		sessionNextSeq: make(map[string]uint64),
		sessionDone:    make(map[string]time.Time),
		keys:           make(map[string]sessionKey),
		keyData:        make(map[string][]byte),
		inboundCh:      make(chan string, 2),
		threshold:      2,
		timeout:        5 * time.Second,
		dataCh:         make(chan []byte, 1),
		stopCh:         make(chan struct{}),
	}

	const sessionID = "late-shard-threshold-session"
	handler.ensureSession(sessionID)
	select {
	case got := <-handler.inboundCh:
		if got != sessionID {
			t.Fatalf("unexpected initial session notification %q", got)
		}
	default:
		t.Fatal("expected initial session notification")
	}
	if err := handler.AddShard(sessionID, shards[0], keyData, nil, len(shards)); err != nil {
		t.Fatalf("add shard 0: %v", err)
	}
	if err := handler.AddShard(sessionID, shards[2], keyData, nil, len(shards)); err != nil {
		t.Fatalf("add shard 2: %v", err)
	}
	reconstructed, err := handler.TryReconstruct(sessionID)
	if err != nil {
		t.Fatalf("reconstruct at threshold: %v", err)
	}
	if !bytes.Equal(reconstructed, original) {
		t.Fatal("reconstructed payload mismatch")
	}

	handler.ensureSession(sessionID)
	if err := handler.AddShard(sessionID, shards[1], keyData, nil, len(shards)); err != nil {
		t.Fatalf("late shard add: %v", err)
	}

	select {
	case got := <-handler.inboundCh:
		t.Fatalf("late shard reopened completed session %q", got)
	default:
	}
	if _, ok := handler.shardBuf[sessionID]; ok {
		t.Fatal("late shard recreated shard buffer for completed session")
	}
}
