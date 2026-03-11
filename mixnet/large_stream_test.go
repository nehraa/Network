package mixnet

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"
)

func TestLargeHeaderOnlyStream1MB(t *testing.T) {
	cfg := &MixnetConfig{
		HopCount:               1,
		CircuitCount:           1,
		Compression:            "gzip",
		UseCESPipeline:         false,
		EncryptionMode:         EncryptionModeHeaderOnly,
		HeaderPaddingEnabled:   false,
		PayloadPaddingStrategy: PaddingStrategyNone,
		EnableAuthTag:          false,
		SelectionMode:          SelectionModeRandom,
		SamplingSize:           3,
		RandomnessFactor:       0.3,
		MaxJitter:              0,
	}

	t.Setenv("MIXNET_MAX_ENCRYPTED_PAYLOAD", "134217728")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	origin, dest, _, cleanup := setupMixnetNetwork(t, ctx, cfg, 3)
	defer cleanup()

	originStream, err := origin.OpenStream(ctx, dest.Host().ID())
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer originStream.Close()

	acceptCh := make(chan *MixStream, 1)
	errCh := make(chan error, 1)
	go func() {
		s, err := dest.AcceptStream(ctx)
		if err != nil {
			errCh <- err
			return
		}
		acceptCh <- s
	}()

	payload := bytes.Repeat([]byte("a"), 1024*1024)
	const chunkSize = 256 * 1024

	for offset := 0; offset < len(payload); offset += chunkSize {
		end := offset + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := originStream.Write(payload[offset:end]); err != nil {
			t.Fatalf("write chunk %d-%d: %v", offset, end, err)
		}
	}

	var destStream *MixStream
	select {
	case err := <-errCh:
		t.Fatalf("accept stream: %v", err)
	case destStream = <-acceptCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for accept stream")
	}
	defer destStream.Close()

	readDone := make(chan []byte, 1)
	go func() {
		buf := make([]byte, chunkSize)
		out := make([]byte, 0, len(payload))
		for len(out) < len(payload) {
			n, err := destStream.Read(buf)
			if err != nil {
				errCh <- err
				return
			}
			out = append(out, buf[:n]...)
		}
		readDone <- out[:len(payload)]
	}()

	select {
	case err := <-errCh:
		t.Fatalf("large stream read failed: %v", err)
	case out := <-readDone:
		if !bytes.Equal(out, payload) {
			t.Fatal("payload mismatch")
		}
	case <-time.After(15 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("timeout reading 1MB payload\n%s", string(buf[:n]))
	}
}

func TestMultiWriteStreamAcrossCircuits(t *testing.T) {
	modes := []struct {
		name string
		mode EncryptionMode
	}{
		{name: "header-only", mode: EncryptionModeHeaderOnly},
		{name: "full", mode: EncryptionModeFull},
	}

	for _, tc := range modes {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &MixnetConfig{
				HopCount:               2,
				CircuitCount:           2,
				Compression:            "gzip",
				UseCESPipeline:         false,
				EncryptionMode:         tc.mode,
				HeaderPaddingEnabled:   false,
				PayloadPaddingStrategy: PaddingStrategyNone,
				EnableAuthTag:          false,
				SelectionMode:          SelectionModeRandom,
				SamplingSize:           6,
				RandomnessFactor:       0.3,
				MaxJitter:              0,
			}

			t.Setenv("MIXNET_MAX_ENCRYPTED_PAYLOAD", "134217728")

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			origin, dest, _, cleanup := setupMixnetNetwork(t, ctx, cfg, 12)
			defer cleanup()

			originStream, err := origin.OpenStream(ctx, dest.Host().ID())
			if err != nil {
				t.Fatalf("open stream: %v", err)
			}
			defer originStream.Close()

			acceptCh := make(chan *MixStream, 1)
			errCh := make(chan error, 1)
			go func() {
				s, err := dest.AcceptStream(ctx)
				if err != nil {
					errCh <- err
					return
				}
				acceptCh <- s
			}()

			pattern := []byte(tc.name + "|")
			payload := bytes.Repeat(pattern, (1024*1024/len(pattern))+1)
			payload = payload[:1024*1024]
			const chunkSize = 256 * 1024

			for offset := 0; offset < len(payload); offset += chunkSize {
				end := offset + chunkSize
				if end > len(payload) {
					end = len(payload)
				}
				if _, err := originStream.Write(payload[offset:end]); err != nil {
					t.Fatalf("write chunk %d-%d: %v", offset, end, err)
				}
			}

			var destStream *MixStream
			select {
			case err := <-errCh:
				t.Fatalf("accept stream: %v", err)
			case destStream = <-acceptCh:
			case <-time.After(5 * time.Second):
				t.Fatal("timeout waiting for accept stream")
			}
			defer destStream.Close()

			readDone := make(chan []byte, 1)
			go func() {
				buf := make([]byte, chunkSize)
				out := make([]byte, 0, len(payload))
				for len(out) < len(payload) {
					n, err := destStream.Read(buf)
					if err != nil {
						errCh <- err
						return
					}
					out = append(out, buf[:n]...)
				}
				readDone <- out[:len(payload)]
			}()

			select {
			case err := <-errCh:
				t.Fatalf("multi-write read failed: %v", err)
			case out := <-readDone:
				if !bytes.Equal(out, payload) {
					t.Fatal("payload mismatch")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("timeout reading multi-write payload")
			}
		})
	}
}
