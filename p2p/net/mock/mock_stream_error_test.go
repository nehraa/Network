package mocknet

import (
	"errors"
	"testing"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/stretchr/testify/require"
)

func TestResetWithErrorPropagatesStreamErrorCodes(t *testing.T) {
	localStream, remoteStream := newConnectedStreamPair()
	err := localStream.ResetWithError(42)
	require.NoError(t, err)

	assertStreamResetError(t, localStream, 42, false)
	assertStreamResetError(t, remoteStream, 42, true)
}

func newConnectedStreamPair() (*stream, *stream) {
	l := newLink(nil, LinkOptions{})
	localConn := &conn{link: l}
	remoteConn := &conn{link: l}

	localStream, remoteStream := newStreamPair()
	localConn.addStream(localStream)
	remoteConn.addStream(remoteStream)
	return localStream, remoteStream
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

func assertStreamResetError(t *testing.T, s network.Stream, code network.StreamErrorCode, remote bool) {
	t.Helper()

	_, err := s.Read(make([]byte, 1))
	requireStreamResetError(t, err, code, remote)

	_, err = s.Write([]byte("x"))
	requireStreamResetError(t, err, code, remote)
}
