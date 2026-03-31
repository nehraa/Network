package mocknet

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
)

var streamCounter atomic.Int64

// stream implements network.Stream
type stream struct {
	rstream *stream
	conn    *conn
	id      int64

	write     *io.PipeWriter
	read      *io.PipeReader
	toDeliver chan *transportObject

	reset  chan error
	close  chan struct{}
	closed chan struct{}

	writeErr         error
	readErr          atomic.Pointer[streamErrorState]
	writeErrOverride atomic.Pointer[streamErrorState]

	protocol atomic.Pointer[protocol.ID]
	stat     network.Stats
}

type streamErrorState struct {
	err error
}

var ErrClosed = errors.New("stream closed")

type transportObject struct {
	msg         []byte
	arrivalTime time.Time
}

func newStreamPair() (*stream, *stream) {
	ra, wb := io.Pipe()
	rb, wa := io.Pipe()

	sa := newStream(wa, ra, network.DirOutbound)
	sb := newStream(wb, rb, network.DirInbound)
	sa.rstream = sb
	sb.rstream = sa
	return sa, sb
}

func newStream(w *io.PipeWriter, r *io.PipeReader, dir network.Direction) *stream {
	s := &stream{
		read:      r,
		write:     w,
		id:        streamCounter.Add(1),
		reset:     make(chan error, 1),
		close:     make(chan struct{}, 1),
		closed:    make(chan struct{}),
		toDeliver: make(chan *transportObject),
		stat:      network.Stats{Direction: dir},
	}
	return s
}

// How to handle errors with writes?
func (s *stream) Write(p []byte) (n int, err error) {
	if err := s.currentWriteErr(); err != nil {
		return 0, err
	}

	l := s.conn.link
	delay := l.GetLatency() + l.RateLimit(len(p))
	t := time.Now().Add(delay)

	// Copy it.
	cpy := make([]byte, len(p))
	copy(cpy, p)

	select {
	case <-s.closed: // bail out if we're closing.
		return 0, s.writeErr
	case s.toDeliver <- &transportObject{msg: cpy, arrivalTime: t}:
	}
	return len(p), nil
}

func (s *stream) ID() string {
	return strconv.FormatInt(s.id, 10)
}

func (s *stream) Protocol() protocol.ID {
	p := s.protocol.Load()
	if p == nil {
		return ""
	}
	return *p
}

func (s *stream) Stat() network.Stats {
	return s.stat
}

func (s *stream) SetProtocol(proto protocol.ID) error {
	s.protocol.Store(&proto)
	return nil
}

func (s *stream) CloseWrite() error {
	s.setWriteErrOverride(ErrClosed)
	select {
	case s.close <- struct{}{}:
	default:
	}
	<-s.closed
	if s.writeErr != ErrClosed {
		return s.writeErr
	}
	return nil
}

func (s *stream) CloseRead() error {
	s.setReadErr(ErrClosed)
	return s.read.CloseWithError(ErrClosed)
}

func (s *stream) Close() error {
	_ = s.CloseRead()
	return s.CloseWrite()
}

func (s *stream) Reset() error {
	return s.ResetWithError(network.StreamNoError)
}

// ResetWithError resets the stream and propagates the provided error code to the
// remote side through the paired pipe closures.
func (s *stream) ResetWithError(errCode network.StreamErrorCode) error {
	localErr := streamResetError(errCode, false)
	remoteErr := streamResetError(errCode, true)

	s.setReadErr(localErr)
	s.setWriteErrOverride(localErr)
	if s.rstream != nil {
		s.rstream.setReadErr(remoteErr)
		s.rstream.setWriteErrOverride(remoteErr)
		select {
		case s.rstream.reset <- remoteErr:
		default:
		}
	}

	// Cancel any pending reads/writes with an error.
	s.write.CloseWithError(remoteErr)
	s.read.CloseWithError(remoteErr)

	select {
	case s.reset <- localErr:
	default:
	}
	<-s.closed
	if s.writeErr == nil {
		s.writeErr = localErr
	}

	// No meaningful error case here.
	return nil
}

func (s *stream) teardown() {
	// at this point, no streams are writing.
	if s.conn != nil {
		s.conn.removeStream(s)
	}

	// Mark as closed.
	close(s.closed)
}

func (s *stream) Conn() network.Conn {
	return s.conn
}

// SetDeadline is a noop for mocknet streams since the underlying pipe
// transport does not support deadlines. Callers should not treat the
// absence of deadline support as an error.
func (s *stream) SetDeadline(_ time.Time) error      { return nil }
func (s *stream) SetReadDeadline(_ time.Time) error  { return nil }
func (s *stream) SetWriteDeadline(_ time.Time) error { return nil }

func (s *stream) Read(b []byte) (int, error) {
	if err := s.currentReadErr(); err != nil {
		return 0, err
	}

	return s.read.Read(b)
}

func (s *stream) startTransport() {
	go s.transport()
}

// transport will grab message arrival times, wait until that time, and
// then write the message out when it is scheduled to arrive
func (s *stream) transport() {
	defer s.teardown()

	bufsize := 256
	buf := new(bytes.Buffer)
	timer := time.NewTimer(0)
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}

	// cleanup
	defer timer.Stop()

	// writeBuf writes the contents of buf through to the s.Writer.
	// done only when arrival time makes sense.
	drainBuf := func() error {
		if buf.Len() > 0 {
			_, err := s.write.Write(buf.Bytes())
			if err != nil {
				return err
			}
			buf.Reset()
		}
		return nil
	}

	// deliverOrWait is a helper func that processes
	// an incoming packet. it waits until the arrival time,
	// and then writes things out.
	deliverOrWait := func(o *transportObject) error {
		buffered := len(o.msg) + buf.Len()

		// Yes, we can end up extending a timer multiple times if we
		// keep on making small writes but that shouldn't be too much of an
		// issue. Fixing that would be painful.
		if !timer.Stop() {
			// FIXME: So, we *shouldn't* need to do this but we hang
			// here if we don't... Go bug?
			select {
			case <-timer.C:
			default:
			}
		}
		delay := time.Until(o.arrivalTime)
		if delay >= 0 {
			timer.Reset(delay)
		} else {
			timer.Reset(0)
		}

		if buffered >= bufsize {
			select {
			case <-timer.C:
			case err := <-s.reset:
				select {
				case s.reset <- err:
				default:
				}
				return err
			}
			if err := drainBuf(); err != nil {
				return err
			}
			// write this message.
			_, err := s.write.Write(o.msg)
			if err != nil {
				return err
			}
		} else {
			buf.Write(o.msg)
		}
		return nil
	}

	for {
		// Reset takes precedent.
		select {
		case err := <-s.reset:
			s.writeErr = err
			return
		default:
		}

		select {
		case err := <-s.reset:
			s.writeErr = err
			return
		case <-s.close:
			if err := drainBuf(); err != nil {
				s.cancelWrite(err)
				return
			}
			s.writeErr = s.write.Close()
			if s.writeErr == nil {
				s.writeErr = ErrClosed
			}
			return
		case o := <-s.toDeliver:
			if err := deliverOrWait(o); err != nil {
				s.cancelWrite(err)
				return
			}
		case <-timer.C: // ok, due to write it out.
			if err := drainBuf(); err != nil {
				s.cancelWrite(err)
				return
			}
		}
	}
}

func (s *stream) Scope() network.StreamScope {
	return &network.NullScope{}
}

func (s *stream) cancelWrite(err error) {
	s.write.CloseWithError(err)
	s.writeErr = err
}

func (s *stream) setReadErr(err error) {
	s.readErr.Store(&streamErrorState{err: err})
}

func (s *stream) currentReadErr() error {
	state := s.readErr.Load()
	if state == nil {
		return nil
	}
	return state.err
}

func (s *stream) setWriteErrOverride(err error) {
	s.writeErrOverride.Store(&streamErrorState{err: err})
}

func (s *stream) currentWriteErr() error {
	state := s.writeErrOverride.Load()
	if state == nil {
		return nil
	}
	return state.err
}

func streamResetError(code network.StreamErrorCode, remote bool) error {
	return &network.StreamError{ErrorCode: code, Remote: remote}
}
