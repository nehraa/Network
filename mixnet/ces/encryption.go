package ces

import (
	cryptorand "crypto/rand"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/flynn/noise"
)

// noiseSuite is the cipher suite used for all layered encryption.
var noiseSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)

// LayeredEncrypter handles layered onion encryption using Noise Protocol primitives
type LayeredEncrypter struct {
	hopCount int
}

// EncryptionKey represents key material for one encryption layer
type EncryptionKey struct {
	Key          []byte
	EphemeralPub []byte
	Destination  string
}

func NewLayeredEncrypter(hopCount int) *LayeredEncrypter {
	return &LayeredEncrypter{hopCount: hopCount}
}

// deriveKey uses HMAC-SHA256 HKDF to derive a 32-byte key from inputKeyMaterial.
func deriveKey(inputKeyMaterial []byte) ([]byte, error) {
	// HKDF-Extract with zero chaining key
	ck := make([]byte, 32)
	mac1 := hmac.New(sha256.New, ck)
	mac1.Write(inputKeyMaterial)
	prk := mac1.Sum(nil)
	// HKDF-Expand: T(1) = HMAC(prk, 0x01)
	mac2 := hmac.New(sha256.New, prk)
	mac2.Write([]byte{0x01})
	return mac2.Sum(nil)[:32], nil
}

// Encrypt encrypts data with Noise-framework-based layered (onion) encryption.
func (e *LayeredEncrypter) Encrypt(plaintext []byte, destinations []string) ([]byte, []*EncryptionKey, error) {
	if len(destinations) != e.hopCount {
		return nil, nil, fmt.Errorf("expected %d destinations, got %d", e.hopCount, len(destinations))
	}

	keys := make([]*EncryptionKey, e.hopCount)

	for i := 0; i < e.hopCount; i++ {
		kp, err := noiseSuite.GenerateKeypair(cryptorand.Reader)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to generate keypair for hop %d: %w", i, err)
		}
		k, err := deriveKey(kp.Private)
		// Securely erase the ephemeral private key after derivation (Req 16.3)
		SecureEraseBytes(kp.Private)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to derive key for hop %d: %w", i, err)
		}
		keys[i] = &EncryptionKey{
			Key:          k,
			EphemeralPub: kp.Public,
			Destination:  destinations[i],
		}
	}

	currentData := plaintext
	for i := e.hopCount - 1; i >= 0; i-- {
		dest := destinations[i]
		// Header: [dest_len: 2 bytes][dest_bytes][ephemeral_pub: 32 bytes]
		header := make([]byte, 2+len(dest)+32)
		binary.LittleEndian.PutUint16(header[0:2], uint16(len(dest)))
		copy(header[2:], dest)
		copy(header[2+len(dest):], keys[i].EphemeralPub)

		payload := append(header, currentData...)

		// Encrypt with Noise CipherChaChaPoly
		var nonceArr [8]byte
		if _, err := io.ReadFull(cryptorand.Reader, nonceArr[:]); err != nil {
			return nil, nil, fmt.Errorf("failed to generate nonce for hop %d: %w", i, err)
		}
		nonce := binary.LittleEndian.Uint64(nonceArr[:])
		var cipherKey [32]byte
		copy(cipherKey[:], keys[i].Key)
		cipher := noiseSuite.Cipher(cipherKey)

		encrypted := cipher.Encrypt(nil, nonce, nil, payload)

		// Prepend: [nonce: 8 bytes][ciphertext]
		out := make([]byte, 8+len(encrypted))
		copy(out[:8], nonceArr[:])
		copy(out[8:], encrypted)
		currentData = out
	}

	return currentData, keys, nil
}

// Decrypt decrypts layered encrypted data produced by Encrypt.
func (e *LayeredEncrypter) Decrypt(ciphertext []byte, keys []*EncryptionKey) ([]byte, error) {
	if len(keys) != e.hopCount {
		return nil, fmt.Errorf("expected %d keys, got %d", e.hopCount, len(keys))
	}

	currentData := ciphertext

	for i := 0; i < e.hopCount; i++ {
		if len(currentData) < 8 {
			return nil, fmt.Errorf("ciphertext too short for hop %d", i)
		}

		nonce := binary.LittleEndian.Uint64(currentData[:8])
		ciphered := currentData[8:]

		var cipherKey [32]byte
		copy(cipherKey[:], keys[i].Key)
		cipher := noiseSuite.Cipher(cipherKey)

		plaintext, err := cipher.Decrypt(nil, nonce, nil, ciphered)
		if err != nil {
			return nil, fmt.Errorf("decryption failed for hop %d: %w", i, err)
		}

		// Parse header: [dest_len: 2][dest_bytes][ephemeral_pub: 32][payload...]
		if len(plaintext) < 2 {
			return nil, fmt.Errorf("invalid header at hop %d", i)
		}
		destLen := int(binary.LittleEndian.Uint16(plaintext[0:2]))
		headerSize := 2 + destLen + 32
		if len(plaintext) < headerSize {
			return nil, fmt.Errorf("invalid header size at hop %d", i)
		}
		currentData = plaintext[headerSize:]
	}

	return currentData, nil
}

func SecureEraseBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// SecureErase implements the Eraser interface (Req 16.3).
func (e *LayeredEncrypter) SecureErase() {}

// EraseKeys zeroes out all key material in the provided keys slice (Req 16.3).
func EraseKeys(keys []*EncryptionKey) {
	for _, k := range keys {
		if k != nil {
			SecureEraseBytes(k.Key)
			SecureEraseBytes(k.EphemeralPub)
		}
	}
}

// HopCount returns the number of encryption layers
func (e *LayeredEncrypter) HopCount() int { return e.hopCount }

// Eraser interface for secure key erasure
type Eraser interface {
	SecureErase()
}
