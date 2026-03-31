package hub

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2pwebrtc "github.com/libp2p/go-libp2p/p2p/transport/webrtc"

	"github.com/stretchr/testify/require"
)

type realTransportCase struct {
	name           string
	listenAddr     string
	wantTransport  string
	expectSecurity bool
	options        []libp2p.Option
}

func TestHubRealTransportMatrix(t *testing.T) {
	t.Parallel()

	cases := []realTransportCase{
		{name: "tcp", listenAddr: "/ip4/127.0.0.1/tcp/0", wantTransport: "tcp", expectSecurity: true},
		{name: "quic", listenAddr: "/ip4/127.0.0.1/udp/0/quic-v1", wantTransport: "quic-v1"},
		{name: "websocket", listenAddr: "/ip4/127.0.0.1/tcp/0/ws", wantTransport: "websocket", expectSecurity: true},
		{name: "webtransport", listenAddr: "/ip4/127.0.0.1/udp/0/quic-v1/webtransport", wantTransport: "webtransport"},
		{
			name:          "webrtc-direct",
			listenAddr:    "/ip4/127.0.0.1/udp/0/webrtc-direct",
			wantTransport: "webrtc-direct",
			options:       []libp2p.Option{libp2p.Transport(libp2pwebrtc.New)},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			h1 := newConfiguredRealHost(t, tc.listenAddr, tc.options...)
			defer h1.Close()

			h2 := newConfiguredRealHost(t, tc.listenAddr, tc.options...)
			defer h2.Close()
			h2.SetStreamHandler(testProtocol, func(stream network.Stream) {
				go func() {
					defer stream.Close()
					_, _ = io.Copy(stream, stream)
				}()
			})

			hub, err := New(h1, Config{ProtocolID: testProtocol, PingInterval: 25 * time.Millisecond})
			require.NoError(t, err)
			defer hub.Close()

			receptor, err := hub.CreateReceptor(context.Background(), peer.AddrInfo{ID: h2.ID(), Addrs: h2.Addrs()})
			require.NotNil(t, receptor)
			require.NoError(t, err)
			payload := []byte("transport-" + tc.name)

			require.Eventually(t, func() bool {
				_, sendErr := receptor.Send(context.Background(), payload)
				if sendErr != nil {
					_ = hub.OpenStream(context.Background(), receptor.ID())
					return false
				}
				return true
			}, 10*time.Second, 50*time.Millisecond)

			received := waitForEvent(t, hub.Events(), 10*time.Second, func(evt Event) bool {
				return evt.Kind == EventKindDataReceived &&
					evt.ReceptorID == receptor.ID() &&
					bytes.Equal(evt.Data, payload)
			})

			require.Equal(t, tc.wantTransport, received.Snapshot.Transport)
			require.NotEmpty(t, received.Snapshot.ConnectionID)
			require.NotZero(t, received.Snapshot.ConnectionCount)
			require.NotZero(t, received.Snapshot.ConnectionOpenedAt)
			require.Equal(t, "ip4", received.Snapshot.TransportDetails.AddressFamily)
			require.NotEmpty(t, received.Snapshot.TransportDetails.ProtocolStack)
			if tc.expectSecurity {
				require.NotEmpty(t, received.Snapshot.SecurityProtocol)
			}
			switch tc.wantTransport {
			case "tcp":
				require.False(t, received.Snapshot.TransportDetails.DetailedMetricsAvailable)
			case "websocket":
				require.NotNil(t, received.Snapshot.TransportDetails.WebSocket)
				require.False(t, received.Snapshot.TransportDetails.DetailedMetricsAvailable)
			case "quic-v1":
				require.NotNil(t, received.Snapshot.TransportDetails.QUIC)
				require.True(t, received.Snapshot.TransportDetails.DetailedMetricsAvailable)
				require.NotEmpty(t, received.Snapshot.TransportDetails.QUIC.Version)
			case "webtransport":
				require.NotNil(t, received.Snapshot.TransportDetails.WebTransport)
				require.True(t, received.Snapshot.TransportDetails.DetailedMetricsAvailable)
			case "webrtc-direct":
				require.NotNil(t, received.Snapshot.TransportDetails.WebRTC)
				require.True(t, received.Snapshot.TransportDetails.DetailedMetricsAvailable)
				require.NotEmpty(t, received.Snapshot.TransportDetails.WebRTC.PeerConnectionState)
			}
		})
	}
}

func newConfiguredRealHost(t *testing.T, listenAddr string, opts ...libp2p.Option) host.Host {
	t.Helper()

	allOpts := []libp2p.Option{libp2p.ListenAddrStrings(listenAddr)}
	allOpts = append(allOpts, opts...)
	h, err := libp2p.New(allOpts...)
	require.NoError(t, err)
	return h
}
