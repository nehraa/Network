//go:build go1.25

package hub

import (
	"context"
	"io"
	"math"
	"testing"
	"time"

	"testing/synctest"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/x/simlibp2p"
	"github.com/marcopolo/simnet"
	"github.com/stretchr/testify/require"
)

func TestMetricsUpdatedTracksRTT(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const latency = 10 * time.Millisecond
		router := &simnet.Simnet{LatencyFunc: simnet.StaticLatency(latency / 2)}

		linkSettings := simnet.NodeBiDiLinkSettings{
			Downlink: simnet.LinkSettings{BitsPerSecond: 10 * simlibp2p.OneMbps},
			Uplink:   simnet.LinkSettings{BitsPerSecond: 10 * simlibp2p.OneMbps},
		}

		hostA := simlibp2p.MustNewHost(t,
			libp2p.ListenAddrStrings("/ip4/1.0.0.1/udp/8000/quic-v1"),
			libp2p.DisableIdentifyAddressDiscovery(),
			simlibp2p.QUICSimnet(router, linkSettings),
		)
		hostB := simlibp2p.MustNewHost(t,
			libp2p.ListenAddrStrings("/ip4/1.0.0.2/udp/8000/quic-v1"),
			libp2p.DisableIdentifyAddressDiscovery(),
			simlibp2p.QUICSimnet(router, linkSettings),
		)
		defer hostA.Close()
		defer hostB.Close()

		router.Start()
		defer router.Close()

		hostB.SetStreamHandler(testProtocol, func(stream network.Stream) {
			go func() {
				defer stream.Close()
				_, _ = io.Copy(io.Discard, stream)
			}()
		})

		require.NoError(t, hostA.Connect(context.Background(), peer.AddrInfo{ID: hostB.ID(), Addrs: hostB.Addrs()}))

		hubA, err := New(hostA, Config{ProtocolID: testProtocol, PingInterval: 25 * time.Millisecond})
		require.NoError(t, err)
		defer hubA.Close()

		receptor, err := hubA.CreateReceptor(context.Background(), peer.AddrInfo{ID: hostB.ID(), Addrs: hostB.Addrs()})
		require.NotNil(t, receptor)
		require.NoError(t, err)

		metrics := waitForEvent(t, hubA.Events(), time.Second, func(evt Event) bool {
			return evt.Kind == EventKindMetricsUpdated && evt.ReceptorID == receptor.ID()
		})

		expectedCandidates := []time.Duration{latency, latency * 2}
		bestRatio := math.MaxFloat64
		for _, expected := range expectedCandidates {
			diff := math.Abs(float64(metrics.Snapshot.LastRTT - expected))
			ratio := diff / float64(expected)
			if ratio < bestRatio {
				bestRatio = ratio
			}
		}
		require.Less(t, bestRatio, 0.25)
		require.NotZero(t, metrics.Snapshot.LatencyEWMA)
	})
}

func TestMetricsUpdatedTracksPingTimeoutFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const latency = 20 * time.Millisecond
		router := &simnet.Simnet{LatencyFunc: simnet.StaticLatency(latency / 2)}

		linkSettings := simnet.NodeBiDiLinkSettings{
			Downlink: simnet.LinkSettings{BitsPerSecond: 10 * simlibp2p.OneMbps},
			Uplink:   simnet.LinkSettings{BitsPerSecond: 10 * simlibp2p.OneMbps},
		}

		hostA := simlibp2p.MustNewHost(t,
			libp2p.ListenAddrStrings("/ip4/1.0.0.1/udp/8100/quic-v1"),
			libp2p.DisableIdentifyAddressDiscovery(),
			simlibp2p.QUICSimnet(router, linkSettings),
		)
		hostB := simlibp2p.MustNewHost(t,
			libp2p.ListenAddrStrings("/ip4/1.0.0.2/udp/8100/quic-v1"),
			libp2p.DisableIdentifyAddressDiscovery(),
			simlibp2p.QUICSimnet(router, linkSettings),
		)
		defer hostA.Close()
		defer hostB.Close()

		router.Start()
		defer router.Close()

		hostB.SetStreamHandler(testProtocol, func(stream network.Stream) {
			go func() {
				defer stream.Close()
				_, _ = io.Copy(io.Discard, stream)
			}()
		})

		require.NoError(t, hostA.Connect(context.Background(), peer.AddrInfo{ID: hostB.ID(), Addrs: hostB.Addrs()}))

		hubA, err := New(hostA, Config{
			ProtocolID:   testProtocol,
			PingInterval: 25 * time.Millisecond,
			PingTimeout:  time.Millisecond,
		})
		require.NoError(t, err)
		defer hubA.Close()

		receptor, err := hubA.CreateReceptor(context.Background(), peer.AddrInfo{ID: hostB.ID(), Addrs: hostB.Addrs()})
		require.NotNil(t, receptor)
		require.NoError(t, err)

		update := waitForMetric(t, hubA.Metrics(), time.Second, func(update MetricUpdate) bool {
			return update.Kind == MetricKindPingFailed && update.ReceptorID == receptor.ID()
		})
		require.Greater(t, update.Snapshot.PingFailureCount, uint64(0))
		require.NotEmpty(t, update.Snapshot.LastError)
	})
}
