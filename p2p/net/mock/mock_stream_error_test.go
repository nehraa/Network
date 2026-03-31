package mocknet

import (
	"context"
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/stretchr/testify/require"
)

func TestResetWithErrorPropagatesStreamErrorCodes(t *testing.T) {
	mn, err := FullMeshConnected(2)
	require.NoError(t, err)
	defer mn.Close()

	hosts := mn.Hosts()
	clientNet := hosts[0].Network()
	serverNet := hosts[1].Network()

	remoteStreamCh := make(chan network.Stream, 1)
	serverNet.SetStreamHandler(func(s network.Stream) {
		remoteStreamCh <- s
	})

	localStream, err := clientNet.NewStream(context.Background(), hosts[1].ID())
	require.NoError(t, err)

	remoteStream := <-remoteStreamCh
	err = localStream.ResetWithError(42)
	require.NoError(t, err)

	_, err = localStream.Write([]byte("x"))
	requireStreamResetError(t, err, 42, false)

	_, err = remoteStream.Read(make([]byte, 1))
	requireStreamResetError(t, err, 42, true)
}

func requireStreamResetError(t *testing.T, err error, code network.StreamErrorCode, remote bool) {
	t.Helper()

	require.Error(t, err)
	require.ErrorIs(t, err, network.ErrReset)

	var streamErr *network.StreamError
	require.True(t, errors.As(err, &streamErr))
	require.Equal(t, code, streamErr.ErrorCode)
	require.Equal(t, remote, streamErr.Remote)
}
