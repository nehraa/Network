package mixnet

import (
	"bytes"
	"testing"
)

func TestEncryptSessionShardsWithKeyRoundTrip(t *testing.T) {
	session, err := newSessionKey(sessionCryptoModePerShardStream)
	if err != nil {
		t.Fatalf("newSessionKey() error = %v", err)
	}

	plaintext := bytes.Repeat([]byte("mixnet-session-shard-"), 64)
	shards, err := encryptSessionShardsWithKey(plaintext, session, 8, "stream-7")
	if err != nil {
		t.Fatalf("encryptSessionShardsWithKey() error = %v", err)
	}
	if len(shards) != 8 {
		t.Fatalf("len(shards) = %d, want 8", len(shards))
	}

	var reconstructed []byte
	for _, shard := range shards {
		decrypted, err := decryptSessionShardPayloadWithKey(shard.Data, session, shard.Index, "stream-7")
		if err != nil {
			t.Fatalf("decryptSessionShardPayloadWithKey() error = %v", err)
		}
		reconstructed = append(reconstructed, decrypted...)
	}

	if !bytes.Equal(reconstructed, plaintext) {
		t.Fatal("session shard round trip mismatch")
	}
}
