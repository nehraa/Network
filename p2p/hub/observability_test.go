package hub

import (
	"bytes"
	"context"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stretchr/testify/require"
)

func TestMetricsChannelPublishesFirstPacket(t *testing.T) {
	mn, h1, h2 := newMockHosts(t)
	defer mn.Close()

	hub1, err := New(h1, Config{ProtocolID: testProtocol, MetricsBufferSize: 32})
	require.NoError(t, err)
	defer hub1.Close()

	hub2, err := New(h2, Config{ProtocolID: testProtocol, MetricsBufferSize: 32})
	require.NoError(t, err)
	defer hub2.Close()

	receptor1, receptor2 := createPairedReceptors(t, hub1, hub2, h1, h2)
	drainMetrics(hub2.Metrics())

	payload := []byte("metrics-first-packet")
	require.Eventually(t, func() bool {
		_, sendErr := receptor1.Send(context.Background(), payload)
		if sendErr != nil {
			_ = hub1.OpenStream(context.Background(), receptor1.ID())
			return false
		}
		return true
	}, 2*time.Second, 20*time.Millisecond)

	update := waitForMetric(t, hub2.Metrics(), 2*time.Second, func(update MetricUpdate) bool {
		return update.Kind == MetricKindFirstPacket && update.ReceptorID == receptor2.ID()
	})
	require.Equal(t, receptor2.ID(), update.ReceptorID)
	require.Equal(t, uint64(1), update.Snapshot.ReceiveOperationCount)
	require.Equal(t, uint64(len(payload)), update.Snapshot.BytesReceived)
	require.NotZero(t, update.Snapshot.FirstPacketAt)
}

func TestEventBackpressureResetsStreamAndTracksDrops(t *testing.T) {
	mn, h1, h2 := newMockHosts(t)
	defer mn.Close()

	hub1, err := New(h1, Config{
		ProtocolID:        testProtocol,
		EventBufferSize:   1,
		MetricsBufferSize: 32,
	})
	require.NoError(t, err)
	defer hub1.Close()

	hub2, err := New(h2, Config{
		ProtocolID:        testProtocol,
		EventBufferSize:   1,
		MetricsBufferSize: 32,
	})
	require.NoError(t, err)
	defer hub2.Close()

	receptor1, receptor2 := createPairedReceptors(t, hub1, hub2, h1, h2)
	drainEvents(hub2.Events())
	drainMetrics(hub2.Metrics())

	payload := bytes.Repeat([]byte("a"), 256)
	require.Eventually(t, func() bool {
		for range 8 {
			if _, sendErr := receptor1.Send(context.Background(), payload); sendErr != nil {
				_ = hub1.OpenStream(context.Background(), receptor1.ID())
				break
			}
		}

		snapshot, snapshotErr := hub2.Snapshot(receptor2.ID())
		if snapshotErr != nil {
			return false
		}
		return snapshot.BackpressureResets > 0 && snapshot.EventDropCount > 0 && !snapshot.HasActiveStream
	}, 3*time.Second, 20*time.Millisecond)

	update := waitForMetric(t, hub2.Metrics(), 2*time.Second, func(update MetricUpdate) bool {
		return update.Kind == MetricKindBackpressure && update.ReceptorID == receptor2.ID()
	})
	require.Greater(t, update.Snapshot.EventDropCount, uint64(0))
	require.Greater(t, update.Snapshot.BackpressureResets, uint64(0))
	require.False(t, update.Snapshot.HasActiveStream)
}

func TestMetricsBackpressureTracksDroppedUpdates(t *testing.T) {
	mn, h1, h2 := newMockHosts(t)
	defer mn.Close()

	installDiscardHandler(h2, testProtocol)

	hub, err := New(h1, Config{
		ProtocolID:        testProtocol,
		PingInterval:      10 * time.Millisecond,
		MetricsBufferSize: 1,
	})
	require.NoError(t, err)
	defer hub.Close()

	receptor, err := hub.CreateReceptor(context.Background(), peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})
	require.NotNil(t, receptor)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		snapshot, snapshotErr := hub.Snapshot(receptor.ID())
		if snapshotErr != nil {
			return false
		}
		return snapshot.MetricsDropCount > 0
	}, time.Second, 20*time.Millisecond)
}

func TestOpenStreamTracksFailureState(t *testing.T) {
	mn, h1, _ := newMockHosts(t)
	defer mn.Close()

	unreachable, err := libp2p.New(libp2p.NoListenAddrs)
	require.NoError(t, err)
	targetInfo := peer.AddrInfo{ID: unreachable.ID()}
	require.NoError(t, unreachable.Close())

	hub, err := New(h1, Config{ProtocolID: testProtocol})
	require.NoError(t, err)
	defer hub.Close()

	receptor, openErr := hub.CreateReceptor(context.Background(), targetInfo)
	require.NotNil(t, receptor)
	require.Error(t, openErr)

	snapshot, snapshotErr := hub.Snapshot(receptor.ID())
	require.NoError(t, snapshotErr)
	require.Greater(t, snapshot.ConnectAttemptCount, uint64(0))
	require.Greater(t, snapshot.ConnectFailureCount, uint64(0))
	require.NotEmpty(t, snapshot.LastError)
}
