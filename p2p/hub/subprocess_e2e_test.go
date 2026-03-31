package hub

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/stretchr/testify/require"
)

const (
	subprocessHelperEnv = "GO_WANT_HUB_E2E_HELPER"
	subprocessListenEnv = "HUB_E2E_LISTEN_ADDR"
)

type subprocessPeerInfo struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (buffer *lockedBuffer) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.Write(p)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.String()
}

func TestHubSubprocessTransportE2E(t *testing.T) {
	if os.Getenv(subprocessHelperEnv) == "1" {
		runSubprocessPeer(t)
		return
	}

	cases := []realTransportCase{
		{name: "tcp", listenAddr: "/ip4/127.0.0.1/tcp/0", wantTransport: "tcp"},
		{name: "quic", listenAddr: "/ip4/127.0.0.1/udp/0/quic-v1", wantTransport: "quic-v1"},
		{name: "websocket", listenAddr: "/ip4/127.0.0.1/tcp/0/ws", wantTransport: "websocket"},
		{name: "webtransport", listenAddr: "/ip4/127.0.0.1/udp/0/quic-v1/webtransport", wantTransport: "webtransport"},
		{name: "webrtc-direct", listenAddr: "/ip4/127.0.0.1/udp/0/webrtc-direct", wantTransport: "webrtc-direct"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			info, stop := startSubprocessPeer(t, tc.listenAddr)
			defer stop()

			h := newRealHost(t, tc.listenAddr)
			defer h.Close()

			hub, err := New(h, Config{ProtocolID: testProtocol, PingInterval: 25 * time.Millisecond})
			require.NoError(t, err)
			defer hub.Close()

			peerID, err := peer.Decode(info.ID)
			require.NoError(t, err)

			receptor, err := hub.CreateReceptor(context.Background(), peer.AddrInfo{
				ID:    peerID,
				Addrs: stringsToMultiaddrs(t, info.Addrs),
			})
			require.NotNil(t, receptor)
			require.NoError(t, err)
			waitForMetric(t, hub.Metrics(), 10*time.Second, func(update MetricUpdate) bool {
				return update.Kind == MetricKindPingSucceeded && update.ReceptorID == receptor.ID()
			})

			payload := []byte("subprocess-" + tc.name)
			require.Eventually(t, func() bool {
				_, sendErr := receptor.Send(context.Background(), payload)
				if sendErr != nil {
					_ = hub.OpenStream(context.Background(), receptor.ID())
					return false
				}
				return true
			}, 10*time.Second, 50*time.Millisecond)

			received := waitForEvent(t, hub.Events(), 10*time.Second, func(evt Event) bool {
				return evt.Kind == EventKindDataReceived && bytes.Equal(evt.Data, payload)
			})
			require.Equal(t, tc.wantTransport, received.Snapshot.Transport)
			require.NotEmpty(t, received.Snapshot.TransportDetails.ProtocolStack)
		})
	}
}

func runSubprocessPeer(t *testing.T) {
	t.Helper()

	listenAddr := os.Getenv(subprocessListenEnv)
	if listenAddr == "" {
		t.Fatal("missing subprocess listen address")
	}

	h := newRealHost(t, listenAddr)
	defer h.Close()

	h.SetStreamHandler(testProtocol, func(stream network.Stream) {
		go func() {
			defer stream.Close()
			_, _ = io.Copy(stream, stream)
		}()
	})

	require.NoError(t, json.NewEncoder(os.Stdout).Encode(subprocessPeerInfo{
		ID:    h.ID().String(),
		Addrs: multiaddrsToStrings(h.Addrs()),
	}))

	_, _ = io.Copy(io.Discard, os.Stdin)
}

func startSubprocessPeer(t *testing.T, listenAddr string) (subprocessPeerInfo, func()) {
	t.Helper()

	cmd := exec.Command(os.Args[0], "-test.run=^TestHubSubprocessTransportE2E$")
	cmd.Env = append(os.Environ(),
		subprocessHelperEnv+"=1",
		subprocessListenEnv+"="+listenAddr,
	)

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	stderr := &lockedBuffer{}
	cmd.Stderr = stderr

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)

	require.NoError(t, cmd.Start())

	reader := bufio.NewReader(stdout)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err, stderr.String())

	var info subprocessPeerInfo
	require.NoError(t, json.Unmarshal(line, &info), stderr.String())

	stop := func() {
		_ = stdin.Close()
		done := make(chan error, 1)
		go func() {
			done <- cmd.Wait()
		}()

		select {
		case waitErr := <-done:
			require.NoError(t, waitErr, stderr.String())
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("timed out waiting for subprocess peer to exit: %s", stderr.String())
		}
	}
	return info, stop
}
