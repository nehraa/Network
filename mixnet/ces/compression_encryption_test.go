package ces

import (
	"bytes"
	"testing"
)

func TestGzipCompressorRoundTripWithPoolReuse(t *testing.T) {
	t.Parallel()

	compressor := NewCompressor("gzip")
	inputs := [][]byte{
		bytes.Repeat([]byte("mixnet-compress-alpha-"), 32),
		bytes.Repeat([]byte("mixnet-compress-beta-"), 48),
	}

	for _, input := range inputs {
		compressed, err := compressor.Compress(input)
		if err != nil {
			t.Fatalf("Compress() error = %v", err)
		}
		decompressed, err := compressor.Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress() error = %v", err)
		}
		if !bytes.Equal(decompressed, input) {
			t.Fatal("gzip round trip mismatch")
		}
	}
}

func TestLayeredEncrypterRoundTrip(t *testing.T) {
	t.Parallel()

	encrypter := NewLayeredEncrypter(4)
	plaintext := bytes.Repeat([]byte("mixnet-layered-payload-"), 24)
	destinations := []string{"peer-a", "peer-b", "peer-c", "peer-d"}

	ciphertext, keys, err := encrypter.Encrypt(plaintext, destinations)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if len(keys) != len(destinations) {
		t.Fatalf("len(keys) = %d, want %d", len(keys), len(destinations))
	}

	decrypted, err := encrypter.Decrypt(ciphertext, keys)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatal("layered encryption round trip mismatch")
	}
}
