package mixnet

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/libp2p/go-libp2p/mixnet/ces"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	sessionKeySize   = 32
	sessionNonceSize = 24
	sessionKeyDataV1 = sessionNonceSize + sessionKeySize
	sessionKeyDataV2 = 1 + sessionNonceSize + sessionKeySize
)

const (
	sessionCryptoModeWholePayload   byte = 0x00
	sessionCryptoModePerShard       byte = 0x01
	sessionCryptoModeWholeStream    byte = 0x02
	sessionCryptoModePerShardStream byte = 0x03
)

type sessionKey struct {
	Key   []byte
	Nonce []byte
	Mode  byte
}

func encryptSessionPayload(plaintext []byte) ([]byte, []byte, error) {
	session, err := newSessionKey(sessionCryptoModeWholePayload)
	if err != nil {
		return nil, nil, err
	}
	ciphertext, err := encryptSessionPayloadWithKey(plaintext, session, "")
	if err != nil {
		return nil, nil, err
	}
	return ciphertext, encodeSessionKeyData(session), nil
}

func encryptSessionPayloadWithKey(plaintext []byte, session sessionKey, sessionID string) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(session.Key)
	if err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, sessionPayloadNonce(session, sessionID), plaintext, nil)
	return ciphertext, nil
}

func decryptSessionPayloadWithKey(ciphertext []byte, key sessionKey, sessionID string) ([]byte, error) {
	if len(key.Key) != sessionKeySize || len(key.Nonce) != sessionNonceSize {
		return nil, fmt.Errorf("invalid session key material")
	}
	aead, err := chacha20poly1305.NewX(key.Key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, sessionPayloadNonce(key, sessionID), ciphertext, nil)
}

func encryptSessionShardsWithKey(plaintext []byte, session sessionKey, total int, sessionID string) ([]*ces.Shard, error) {
	if total <= 0 {
		return nil, fmt.Errorf("invalid shard count: %d", total)
	}
	aead, err := chacha20poly1305.NewX(session.Key)
	if err != nil {
		return nil, err
	}
	shards, err := shardEvenly(plaintext, total)
	if err != nil {
		return nil, err
	}
	for _, shard := range shards {
		nonce := sessionShardNonce(session, sessionID, shard.Index)
		shard.Data = aead.Seal(nil, nonce, shard.Data, nil)
	}
	return shards, nil
}

func decryptSessionShardPayloadWithKey(ciphertext []byte, key sessionKey, shardIndex int, sessionID string) ([]byte, error) {
	if len(key.Key) != sessionKeySize || len(key.Nonce) != sessionNonceSize {
		return nil, fmt.Errorf("invalid session key material")
	}
	aead, err := chacha20poly1305.NewX(key.Key)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, sessionShardNonce(key, sessionID, shardIndex), ciphertext, nil)
}

func streamSessionMode(mode byte) bool {
	return mode == sessionCryptoModeWholeStream || mode == sessionCryptoModePerShardStream
}

func sessionPayloadNonce(key sessionKey, sessionID string) []byte {
	if !streamSessionMode(key.Mode) {
		return key.Nonce
	}
	return deriveSessionStreamNonce(key.Nonce, streamSessionSequence(sessionID), -1)
}

func sessionShardNonce(key sessionKey, sessionID string, shardIndex int) []byte {
	if !streamSessionMode(key.Mode) {
		return deriveSessionShardNonce(key.Nonce, shardIndex)
	}
	return deriveSessionStreamNonce(key.Nonce, streamSessionSequence(sessionID), shardIndex)
}

func streamSessionSequence(sessionID string) uint64 {
	_, seq, ok := parseStreamWriteSequence(sessionID)
	if !ok {
		return 0
	}
	return seq
}

func deriveSessionStreamNonce(base []byte, seq uint64, shardIndex int) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte("mixnet-stream-session-nonce"))
	_, _ = h.Write(base)
	var buf [12]byte
	binary.LittleEndian.PutUint64(buf[:8], seq)
	if shardIndex >= 0 {
		binary.LittleEndian.PutUint32(buf[8:], uint32(shardIndex+1))
	}
	_, _ = h.Write(buf[:])
	sum := h.Sum(nil)
	nonce := make([]byte, sessionNonceSize)
	copy(nonce, sum[:sessionNonceSize])
	return nonce
}

func encryptSessionShards(plaintext []byte, total int) ([]*ces.Shard, []byte, error) {
	if total <= 0 {
		return nil, nil, fmt.Errorf("invalid shard count: %d", total)
	}
	session, err := newSessionKey(sessionCryptoModePerShard)
	if err != nil {
		return nil, nil, err
	}
	shards, err := encryptSessionShardsWithKey(plaintext, session, total, "")
	if err != nil {
		return nil, nil, err
	}
	return shards, encodeSessionKeyData(session), nil
}

func decryptSessionPayload(ciphertext []byte, key sessionKey) ([]byte, error) {
	return decryptSessionPayloadWithKey(ciphertext, key, "")
}

func decryptSessionShardPayload(ciphertext []byte, key sessionKey, shardIndex int) ([]byte, error) {
	return decryptSessionShardPayloadWithKey(ciphertext, key, shardIndex, "")
}

func newSessionKey(mode byte) (sessionKey, error) {
	key := make([]byte, sessionKeySize)
	nonce := make([]byte, sessionNonceSize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return sessionKey{}, err
	}
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return sessionKey{}, err
	}
	return sessionKey{Key: key, Nonce: nonce, Mode: mode}, nil
}

func deriveSessionShardNonce(base []byte, shardIndex int) []byte {
	nonce := append([]byte(nil), base...)
	tail := binary.LittleEndian.Uint64(nonce[len(nonce)-8:])
	binary.LittleEndian.PutUint64(nonce[len(nonce)-8:], tail^uint64(shardIndex+1))
	return nonce
}

func encodeSessionKeyData(key sessionKey) []byte {
	if key.Mode == sessionCryptoModeWholePayload {
		buf := make([]byte, 0, sessionKeyDataV1)
		buf = append(buf, key.Nonce...)
		buf = append(buf, key.Key...)
		return buf
	}
	buf := make([]byte, 0, sessionKeyDataV2)
	buf = append(buf, key.Mode)
	buf = append(buf, key.Nonce...)
	buf = append(buf, key.Key...)
	return buf
}

func decodeSessionKeyData(data []byte) (sessionKey, error) {
	mode := sessionCryptoModeWholePayload
	switch len(data) {
	case sessionKeyDataV1:
	case sessionKeyDataV2:
		mode = data[0]
		data = data[1:]
	default:
		return sessionKey{}, fmt.Errorf("invalid key data length")
	}
	nonce := make([]byte, sessionNonceSize)
	key := make([]byte, sessionKeySize)
	copy(nonce, data[:sessionNonceSize])
	copy(key, data[sessionNonceSize:])
	return sessionKey{Key: key, Nonce: nonce, Mode: mode}, nil
}
