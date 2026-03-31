package mixnet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

func TestCoverTrafficGeneratorSendsConfiguredPackets(t *testing.T) {
	ctg := NewCoverTrafficGenerator(&CoverTrafficConfig{
		Enabled:    true,
		Interval:   5 * time.Millisecond,
		PacketSize: 32,
	})

	type sentPacket struct {
		peer peer.ID
		size int
	}
	sentCh := make(chan sentPacket, 1)
	ctg.SetSender(func(_ context.Context, target peer.ID, payload []byte) error {
		sentCh <- sentPacket{peer: target, size: len(payload)}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ctg.Start(ctx, func() []peer.ID { return []peer.ID{"relay-a", "relay-b"} })
	defer ctg.Stop()

	select {
	case sent := <-sentCh:
		if sent.peer == "" {
			t.Fatal("expected a target relay")
		}
		if sent.size != 32 {
			t.Fatalf("payload size = %d, want 32", sent.size)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for cover traffic packet")
	}
}

func TestCoverTrafficGeneratorReportsMissingSender(t *testing.T) {
	ctg := NewCoverTrafficGenerator(&CoverTrafficConfig{
		Enabled:    true,
		Interval:   5 * time.Millisecond,
		PacketSize: 16,
	})

	errCh := make(chan error, 1)
	ctg.SetErrorHandler(func(err error) {
		select {
		case errCh <- err:
		default:
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ctg.Start(ctx, func() []peer.ID { return []peer.ID{"relay-a"} })
	defer ctg.Stop()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) && err.Error() != "cover traffic sender is not configured" {
			t.Fatalf("unexpected cover traffic error: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for cover traffic configuration error")
	}
}
