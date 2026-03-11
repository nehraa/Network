// Package relay implements the zero-knowledge packet forwarding for mixnet relay nodes.
package relay

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// ProtocolID is the libp2p protocol identifier for mixnet relaying.
	ProtocolID = "/lib-mix/relay/1.0.0"
	// FinalProtocolID is the protocol identifier used when forwarding to the destination.
	FinalProtocolID = "/lib-mix/1.0.0"

	// MaxPayloadSize is the maximum allowed size for a single packet.
	MaxPayloadSize = 64 * 1024 // 64KB

	// ReadTimeout is the duration after which an inactive relay stream is closed.
	ReadTimeout = 30 * time.Second

	// nonceSize is the nonce size for ChaCha20-Poly1305 (12 bytes for standard, 24 for X)
	nonceSize      = 24 // XChaCha20-Poly1305
	writeChunkSize = 32 * 1024

	frameVersionFullOnion  byte = 0x01
	frameVersionHeaderOnly byte = 0x02
)

const (
	msgTypeData     byte = 0x00
	msgTypeCloseReq byte = 0x01
	msgTypeCloseAck byte = 0x02
)

func MaxEncryptedPayloadSize() int {
	const defaultMultiplier = 4

	if raw := os.Getenv("MIXNET_MAX_ENCRYPTED_PAYLOAD"); raw != "" {
		if size, err := strconv.Atoi(raw); err == nil && size > 0 {
			return size
		}
	}

	return MaxPayloadSize * defaultMultiplier
}

// RelayInfo contains runtime statistics and state for an active relay circuit on this node.
type RelayInfo struct {
	// PeerID is the identifier of the peer that opened the relay stream.
	PeerID peer.ID
	// Stream is the network stream being relayed.
	Stream network.Stream
	// CircuitID is the internal identifier for this relay circuit.
	CircuitID string
	// BytesForwarded is the total number of bytes processed by this relay.
	BytesForwarded int64
	// LastActivity is the timestamp of the last data movement.
	LastActivity time.Time
	mu           sync.Mutex
}

// Handler manages all active relay streams and enforces resource limits.
type Handler struct {
	host              host.Host
	maxBandwidth      int64
	maxCircuits       int
	useRCMgr          bool
	serviceName       string
	activeRelays      map[string]*RelayInfo // circuitID -> relay info
	protocolID        string
	mu                sync.RWMutex
	muKeys            sync.RWMutex
	circuitKeys       map[string][]byte // circuitID -> hop key
	waitBandwidth     func(context.Context, int64) error
	recordBandwidth   func(string, int64)
	reportUtilization func(int)
}

// NewHandler creates a new relay Handler with the specified limits.
func NewHandler(host host.Host, maxCircuits int, maxBandwidth int64) *Handler {
	return &Handler{
		host:         host,
		maxBandwidth: maxBandwidth,
		maxCircuits:  maxCircuits,
		useRCMgr:     true,
		serviceName:  "mixnet-relay",
		activeRelays: make(map[string]*RelayInfo),
		protocolID:   ProtocolID,
		circuitKeys:  make(map[string][]byte),
	}
}

// HandleStream implements the libp2p stream handler for incoming relay requests.
// It performs zero-knowledge forwarding of the encrypted payload to the next hop.
// AC 7.1: Decrypt outermost layer
// AC 7.2: Extract next-hop from decrypted header
func (h *Handler) HandleStream(stream network.Stream) {
	baseCtx := context.Background()
	defer stream.Close()

	// Enforce compatibility circuit limits only when rcmgr integration is disabled.
	h.mu.Lock()
	if !h.useRCMgr && h.maxCircuits > 0 && len(h.activeRelays) >= h.maxCircuits {
		h.mu.Unlock()
		return
	}
	circuitID := fmt.Sprintf("relay-%d", len(h.activeRelays))
	h.activeRelays[circuitID] = &RelayInfo{
		PeerID:       stream.Conn().RemotePeer(),
		Stream:       stream,
		CircuitID:    circuitID,
		LastActivity: time.Now(),
	}
	if h.reportUtilization != nil {
		h.reportUtilization(len(h.activeRelays))
	}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.activeRelays, circuitID)
		if h.reportUtilization != nil {
			h.reportUtilization(len(h.activeRelays))
		}
		h.mu.Unlock()
	}()

	reader := bufio.NewReader(stream)
	var dst network.Stream
	var dstPeer peer.ID
	var dstIsFinal bool

	defer func() {
		if dst != nil {
			_ = dst.Close()
		}
	}()

	for {
		// Set a read deadline so the relay cannot be held open indefinitely (Req 6.3).
		deadline := time.Now().Add(ReadTimeout)
		_ = stream.SetDeadline(deadline)

		circuitID, frameVersion, payloadLen, err := readEncryptedFrameHeader(reader)
		if err != nil {
			return
		}
		frameCtx, cancel := context.WithDeadline(baseCtx, deadline)
		err = func(ctx context.Context) error {
			defer cancel()

			key := h.getCircuitKey(circuitID)
			if len(key) == 0 {
				return fmt.Errorf("missing circuit key")
			}

			if frameVersion == frameVersionHeaderOnly {
				return h.handleHeaderOnlyFrameStream(ctx, reader, circuitID, payloadLen, key, &dst, &dstPeer, &dstIsFinal, stream)
			}

			encPayload, releaseMem, err := readEncryptedFramePayload(reader, stream.Scope(), payloadLen)
			if err != nil {
				return err
			}
			if releaseMem != nil {
				defer releaseMem()
			}
			if h.recordBandwidth != nil {
				h.recordBandwidth("in", int64(len(encPayload)))
			}

			var (
				isFinal     bool
				nextHop     string
				innerHeader []byte
			)

			switch frameVersion {
			case frameVersionFullOnion:
				plaintext, err := decryptHopPayload(key, encPayload)
				if err != nil {
					return err
				}
				parsedFinal, parsedHop, innerPayload, err := parseHopPayload(plaintext)
				if err != nil {
					return err
				}
				isFinal = parsedFinal
				nextHop = parsedHop
				innerHeader = innerPayload
			default:
				return fmt.Errorf("unknown frame version: %d", frameVersion)
			}

			// Determine protocol based on whether this is the final hop.
			nextProto := ProtocolID
			if isFinal {
				nextProto = FinalProtocolID
			}

			// Parse the next hop as a peer ID; fall back to multiaddr parsing.
			nextPeer, err := peer.Decode(nextHop)
			if err != nil {
				if dst != nil {
					_ = dst.Close()
					dst = nil
				}
				return h.forwardByAddressEncrypted(ctx, nextHop, circuitID, frameVersion, innerHeader, nextProto)
			}

			// Keep per-circuit streams open; rotate only when route target/mode changes.
			if dst == nil || dstPeer != nextPeer || dstIsFinal != isFinal {
				if dst != nil {
					_ = dst.Close()
				}
				s, err := h.openStream(ctx, nextPeer, nextProto)
				if err != nil {
					return err
				}
				dst = s
				dstPeer = nextPeer
				dstIsFinal = isFinal
			}

			// Apply bandwidth limit as a rate-limited writer if configured (Req 20.2, 20.4).
			var writer io.Writer = dst
			h.mu.RLock()
			maxBandwidth := h.maxBandwidth
			waitBandwidth := h.waitBandwidth
			recordBandwidth := h.recordBandwidth
			h.mu.RUnlock()
			if maxBandwidth > 0 {
				writer = &rateLimitedWriter{w: dst, bytesPerSec: maxBandwidth}
			}
			if isFinal {
				if _, err := writePayloadWithBandwidth(ctx, writer, innerHeader, writeChunkSize, waitBandwidth, recordBandwidth); err != nil {
					return err
				}
				if len(innerHeader) > 0 && innerHeader[0] == msgTypeCloseReq {
					if err := waitForCloseAck(dst); err != nil {
						return err
					}
					if _, err := stream.Write([]byte{msgTypeCloseAck}); err != nil {
						return err
					}
					_ = dst.Close()
					dst = nil
					return io.EOF
				}
				// The destination handler treats each final-delivery stream as a
				// single message and reads until EOF, so normal data delivery must
				// close the final-hop stream after each payload.
				if dst != nil {
					_ = dst.Close()
					dst = nil
				}
			} else {
				if _, err := writeEncryptedFrame(ctx, writer, circuitID, frameVersion, innerHeader, waitBandwidth, recordBandwidth); err != nil {
					return err
				}
			}

			return nil
		}(frameCtx)
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
	}
}

func (h *Handler) handleHeaderOnlyFrameStream(ctx context.Context, reader *bufio.Reader, circuitID string, payloadLen int, key []byte, dst *network.Stream, dstPeer *peer.ID, dstIsFinal *bool, src network.Stream) error {
	h.mu.RLock()
	recordBandwidth := h.recordBandwidth
	waitBandwidth := h.waitBandwidth
	maxBandwidth := h.maxBandwidth
	h.mu.RUnlock()

	if payloadLen < 4 {
		return fmt.Errorf("header-only payload too short")
	}
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(reader, lenBuf); err != nil {
		return err
	}
	if recordBandwidth != nil {
		recordBandwidth("in", 4)
	}
	headerLen := int(binary.LittleEndian.Uint32(lenBuf))
	if headerLen <= 0 || payloadLen < 4+headerLen {
		return fmt.Errorf("invalid header length")
	}
	encryptedHeader := make([]byte, headerLen)
	if _, err := io.ReadFull(reader, encryptedHeader); err != nil {
		return err
	}
	if recordBandwidth != nil {
		recordBandwidth("in", int64(headerLen))
	}
	plaintext, err := decryptHopPayload(key, encryptedHeader)
	if err != nil {
		return err
	}
	isFinal, nextHop, innerHeader, err := parseHopPayload(plaintext)
	if err != nil {
		return err
	}
	dataPayloadLen := payloadLen - 4 - headerLen

	nextProto := ProtocolID
	if isFinal {
		nextProto = FinalProtocolID
	}

	nextPeerDecoded, err := peer.Decode(nextHop)
	if err != nil {
		if *dst != nil {
			_ = (*dst).Close()
			*dst = nil
		}
		return h.forwardByAddressHeaderOnlyStreaming(ctx, reader, nextHop, circuitID, innerHeader, dataPayloadLen, nextProto, waitBandwidth, recordBandwidth, maxBandwidth)
	}

	if *dst == nil || *dstPeer != nextPeerDecoded || *dstIsFinal != isFinal {
		if *dst != nil {
			_ = (*dst).Close()
		}
		s, err := h.openStream(ctx, nextPeerDecoded, nextProto)
		if err != nil {
			return err
		}
		*dst = s
		*dstPeer = nextPeerDecoded
		*dstIsFinal = isFinal
	}

	var writer io.Writer = *dst
	if maxBandwidth > 0 {
		writer = &rateLimitedWriter{w: *dst, bytesPerSec: maxBandwidth}
	}
	if isFinal {
		if _, err := writePayloadWithBandwidth(ctx, writer, []byte{msgTypeData}, 1, waitBandwidth, recordBandwidth); err != nil {
			return err
		}
		if _, err := writePayloadWithBandwidth(ctx, writer, innerHeader, writeChunkSize, waitBandwidth, recordBandwidth); err != nil {
			return err
		}
		if _, err := pipePayloadWithBandwidth(ctx, reader, writer, dataPayloadLen, waitBandwidth, recordBandwidth); err != nil {
			return err
		}
		if len(innerHeader) > 0 && innerHeader[0] == msgTypeCloseReq {
			if err := waitForCloseAck(*dst); err != nil {
				return err
			}
			if _, err := src.Write([]byte{msgTypeCloseAck}); err != nil {
				return err
			}
			_ = (*dst).Close()
			*dst = nil
			return io.EOF
		}
		if *dst != nil {
			_ = (*dst).Close()
			*dst = nil
		}
		return nil
	}

	if _, err := writeHeaderOnlyFramePrefix(ctx, writer, circuitID, innerHeader, dataPayloadLen, waitBandwidth, recordBandwidth); err != nil {
		return err
	}
	_, err = pipePayloadWithBandwidth(ctx, reader, writer, dataPayloadLen, waitBandwidth, recordBandwidth)
	return err
}

func (h *Handler) openStream(ctx context.Context, nextPeer peer.ID, protoID string) (network.Stream, error) {
	h.mu.RLock()
	host := h.host
	useRCMgr := h.useRCMgr
	serviceName := h.serviceName
	h.mu.RUnlock()

	if host == nil {
		return nil, fmt.Errorf("no host configured")
	}
	if host.Network().Connectedness(nextPeer) != network.Connected {
		connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := host.Connect(connectCtx, peer.AddrInfo{ID: nextPeer}); err != nil {
			return nil, fmt.Errorf("failed to connect to next hop: %w", err)
		}
	}

	// Pre-admit outbound stream with rcmgr so resource policies are enforced centrally.
	if useRCMgr {
		rm := host.Network().ResourceManager()
		scope, err := rm.OpenStream(nextPeer, network.DirOutbound)
		if err != nil {
			return nil, fmt.Errorf("rcmgr rejected outbound stream: %w", err)
		}
		if err := scope.SetProtocol(protocol.ID(protoID)); err != nil {
			scope.Done()
			return nil, fmt.Errorf("rcmgr protocol scope setup failed: %w", err)
		}
		// Best effort. Service names are optional in rcmgr.
		_ = scope.SetService(serviceName)
		defer scope.Done()
	}

	s, err := host.NewStream(ctx, nextPeer, protocol.ID(protoID))
	if err != nil {
		return nil, err
	}
	if flusher, ok := s.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			_ = s.Reset()
			return nil, fmt.Errorf("failed to negotiate stream: %w", err)
		}
	}
	// Tag the stream's own scope with service so resource policies are enforced correctly.
	if useRCMgr {
		_ = s.Scope().SetService(serviceName)
	}
	return s, nil
}

func (h *Handler) forwardByAddressEncrypted(ctx context.Context, addr string, circuitID string, version byte, payload []byte, protoID string) error {
	h.mu.RLock()
	host := h.host
	maxBandwidth := h.maxBandwidth
	waitBandwidth := h.waitBandwidth
	recordBandwidth := h.recordBandwidth
	h.mu.RUnlock()

	if host == nil {
		return fmt.Errorf("no host configured")
	}

	addrInfo, err := peer.AddrInfoFromString(addr)
	if err != nil {
		return fmt.Errorf("failed to parse address: %w", err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := host.Connect(connectCtx, *addrInfo); err != nil {
		return fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	dst, err := host.NewStream(ctx, addrInfo.ID, protocol.ID(protoID))
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer dst.Close()
	if flusher, ok := dst.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			_ = dst.Reset()
			return fmt.Errorf("failed to negotiate stream: %w", err)
		}
	}

	var writer io.Writer = dst
	if maxBandwidth > 0 {
		writer = &rateLimitedWriter{w: dst, bytesPerSec: maxBandwidth}
	}
	if waitBandwidth != nil {
		if err := waitBandwidth(ctx, int64(len(payload))); err != nil {
			return err
		}
	}

	_, err = writeEncryptedFrame(ctx, writer, circuitID, version, payload, waitBandwidth, recordBandwidth)
	return err
}

func (h *Handler) forwardByAddressHeaderOnlyEncrypted(ctx context.Context, addr string, circuitID string, encryptedHeader []byte, dataPayload []byte, protoID string) error {
	h.mu.RLock()
	host := h.host
	maxBandwidth := h.maxBandwidth
	waitBandwidth := h.waitBandwidth
	recordBandwidth := h.recordBandwidth
	h.mu.RUnlock()

	if host == nil {
		return fmt.Errorf("no host configured")
	}

	addrInfo, err := peer.AddrInfoFromString(addr)
	if err != nil {
		return fmt.Errorf("failed to parse address: %w", err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := host.Connect(connectCtx, *addrInfo); err != nil {
		return fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	dst, err := host.NewStream(ctx, addrInfo.ID, protocol.ID(protoID))
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer dst.Close()
	if flusher, ok := dst.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			_ = dst.Reset()
			return fmt.Errorf("failed to negotiate stream: %w", err)
		}
	}

	var writer io.Writer = dst
	if maxBandwidth > 0 {
		writer = &rateLimitedWriter{w: dst, bytesPerSec: maxBandwidth}
	}

	_, err = writeHeaderOnlyFrame(ctx, writer, circuitID, encryptedHeader, dataPayload, waitBandwidth, recordBandwidth)
	return err
}

func (h *Handler) forwardByAddressHeaderOnlyStreaming(ctx context.Context, reader *bufio.Reader, addr string, circuitID string, encryptedHeader []byte, dataPayloadLen int, protoID string, waitBandwidth func(context.Context, int64) error, recordBandwidth func(string, int64), maxBandwidth int64) error {
	h.mu.RLock()
	host := h.host
	h.mu.RUnlock()

	if host == nil {
		return fmt.Errorf("no host configured")
	}

	addrInfo, err := peer.AddrInfoFromString(addr)
	if err != nil {
		return fmt.Errorf("failed to parse address: %w", err)
	}

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := host.Connect(connectCtx, *addrInfo); err != nil {
		return fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	dst, err := host.NewStream(ctx, addrInfo.ID, protocol.ID(protoID))
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer dst.Close()
	if flusher, ok := dst.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			_ = dst.Reset()
			return fmt.Errorf("failed to negotiate stream: %w", err)
		}
	}

	var writer io.Writer = dst
	if maxBandwidth > 0 {
		writer = &rateLimitedWriter{w: dst, bytesPerSec: maxBandwidth}
	}
	if _, err := writeHeaderOnlyFramePrefix(ctx, writer, circuitID, encryptedHeader, dataPayloadLen, waitBandwidth, recordBandwidth); err != nil {
		return err
	}
	_, err = pipePayloadWithBandwidth(ctx, reader, writer, dataPayloadLen, waitBandwidth, recordBandwidth)
	return err
}

func waitForCloseAck(stream network.Stream) error {
	_ = stream.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 1)
	if _, err := io.ReadFull(stream, buf); err != nil {
		return err
	}
	if buf[0] != msgTypeCloseAck {
		return fmt.Errorf("unexpected close ack: %x", buf[0])
	}
	return nil
}

func readEncryptedFrameHeader(r *bufio.Reader) (string, byte, int, error) {
	cidLen, err := r.ReadByte()
	if err != nil {
		return "", 0, 0, err
	}
	if cidLen == 0 {
		return "", 0, 0, fmt.Errorf("empty circuit id")
	}
	cid := make([]byte, int(cidLen))
	if _, err := io.ReadFull(r, cid); err != nil {
		return "", 0, 0, err
	}
	version, err := r.ReadByte()
	if err != nil {
		return "", 0, 0, err
	}
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(r, lenBuf); err != nil {
		return "", 0, 0, err
	}
	payloadLen := int(binary.LittleEndian.Uint32(lenBuf))
	maxPayloadSize := MaxEncryptedPayloadSize()
	if payloadLen <= 0 || payloadLen > maxPayloadSize {
		return "", 0, 0, fmt.Errorf("invalid encrypted payload length")
	}
	return string(cid), version, payloadLen, nil
}

func readEncryptedFramePayload(r *bufio.Reader, scope network.StreamScope, payloadLen int) ([]byte, func(), error) {
	release := func() {}
	if scope != nil {
		if err := scope.ReserveMemory(payloadLen, network.ReservationPriorityMedium); err != nil {
			return nil, nil, fmt.Errorf("rcmgr inbound memory reservation failed: %w", err)
		}
		release = func() { scope.ReleaseMemory(payloadLen) }
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		release()
		return nil, nil, err
	}
	return payload, release, nil
}

func writeEncryptedFrame(ctx context.Context, w io.Writer, circuitID string, version byte, payload []byte, waitBandwidth func(context.Context, int64) error, recordBandwidth func(string, int64)) (int, error) {
	if len(circuitID) == 0 || len(circuitID) > 255 {
		return 0, fmt.Errorf("invalid circuit id")
	}
	maxPayloadSize := MaxEncryptedPayloadSize()
	if len(payload) > maxPayloadSize {
		return 0, fmt.Errorf("payload too large: %d bytes exceeds limit of %d", len(payload), maxPayloadSize)
	}
	header := make([]byte, 1+len(circuitID)+1+4)
	header[0] = byte(len(circuitID))
	copy(header[1:], []byte(circuitID))
	header[1+len(circuitID)] = version
	binary.LittleEndian.PutUint32(header[1+len(circuitID)+1:], uint32(len(payload)))
	hn, err := writePayloadWithBandwidth(ctx, w, header, len(header), waitBandwidth, recordBandwidth)
	if err != nil {
		return hn, err
	}
	pn, err := writePayloadWithBandwidth(ctx, w, payload, writeChunkSize, waitBandwidth, recordBandwidth)
	return hn + pn, err
}

func writeHeaderOnlyFrame(ctx context.Context, w io.Writer, circuitID string, encryptedHeader []byte, dataPayload []byte, waitBandwidth func(context.Context, int64) error, recordBandwidth func(string, int64)) (int, error) {
	if len(circuitID) == 0 || len(circuitID) > 255 {
		return 0, fmt.Errorf("invalid circuit id")
	}
	if len(encryptedHeader) == 0 {
		return 0, fmt.Errorf("missing header-only header")
	}
	payloadLen := 4 + len(encryptedHeader) + len(dataPayload)
	maxPayloadSize := MaxEncryptedPayloadSize()
	if payloadLen > maxPayloadSize {
		return 0, fmt.Errorf("payload too large: %d bytes exceeds limit of %d", payloadLen, maxPayloadSize)
	}

	total, err := writeHeaderOnlyFramePrefix(ctx, w, circuitID, encryptedHeader, len(dataPayload), waitBandwidth, recordBandwidth)
	if err != nil {
		return total, err
	}
	n, err := writePayloadWithBandwidth(ctx, w, dataPayload, writeChunkSize, waitBandwidth, recordBandwidth)
	total += n
	return total, err
}

func writeHeaderOnlyFramePrefix(ctx context.Context, w io.Writer, circuitID string, encryptedHeader []byte, dataPayloadLen int, waitBandwidth func(context.Context, int64) error, recordBandwidth func(string, int64)) (int, error) {
	if len(circuitID) == 0 || len(circuitID) > 255 {
		return 0, fmt.Errorf("invalid circuit id")
	}
	if len(encryptedHeader) == 0 {
		return 0, fmt.Errorf("missing header-only header")
	}
	payloadLen := 4 + len(encryptedHeader) + dataPayloadLen
	maxPayloadSize := MaxEncryptedPayloadSize()
	if payloadLen > maxPayloadSize {
		return 0, fmt.Errorf("payload too large: %d bytes exceeds limit of %d", payloadLen, maxPayloadSize)
	}

	frameHeader := make([]byte, 1+len(circuitID)+1+4)
	frameHeader[0] = byte(len(circuitID))
	copy(frameHeader[1:], []byte(circuitID))
	frameHeader[1+len(circuitID)] = frameVersionHeaderOnly
	binary.LittleEndian.PutUint32(frameHeader[1+len(circuitID)+1:], uint32(payloadLen))

	headerOnlyPrefix := make([]byte, 4)
	binary.LittleEndian.PutUint32(headerOnlyPrefix, uint32(len(encryptedHeader)))

	total, err := writePayloadWithBandwidth(ctx, w, frameHeader, len(frameHeader), waitBandwidth, recordBandwidth)
	if err != nil {
		return total, err
	}
	n, err := writePayloadWithBandwidth(ctx, w, headerOnlyPrefix, len(headerOnlyPrefix), waitBandwidth, recordBandwidth)
	total += n
	if err != nil {
		return total, err
	}
	n, err = writePayloadWithBandwidth(ctx, w, encryptedHeader, writeChunkSize, waitBandwidth, recordBandwidth)
	total += n
	return total, err
}

func writeHeaderOnlyFinalPayload(ctx context.Context, w io.Writer, controlHeader []byte, dataPayload []byte, waitBandwidth func(context.Context, int64) error, recordBandwidth func(string, int64)) (int, error) {
	total, err := writePayloadWithBandwidth(ctx, w, []byte{msgTypeData}, 1, waitBandwidth, recordBandwidth)
	if err != nil {
		return total, err
	}
	n, err := writePayloadWithBandwidth(ctx, w, controlHeader, writeChunkSize, waitBandwidth, recordBandwidth)
	total += n
	if err != nil {
		return total, err
	}
	n, err = writePayloadWithBandwidth(ctx, w, dataPayload, writeChunkSize, waitBandwidth, recordBandwidth)
	total += n
	return total, err
}

func pipePayloadWithBandwidth(ctx context.Context, r *bufio.Reader, w io.Writer, remaining int, waitBandwidth func(context.Context, int64) error, recordBandwidth func(string, int64)) (int, error) {
	if remaining < 0 {
		return 0, fmt.Errorf("invalid payload length")
	}
	buf := make([]byte, writeChunkSize)
	total := 0
	for remaining > 0 {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		chunkLen := remaining
		if chunkLen > len(buf) {
			chunkLen = len(buf)
		}
		n, err := io.ReadFull(r, buf[:chunkLen])
		if n > 0 && recordBandwidth != nil {
			recordBandwidth("in", int64(n))
		}
		if err != nil {
			return total, err
		}
		written, err := writePayloadWithBandwidth(ctx, w, buf[:n], n, waitBandwidth, recordBandwidth)
		total += written
		if err != nil {
			return total, err
		}
		remaining -= n
	}
	return total, nil
}

func writePayloadWithBandwidth(ctx context.Context, w io.Writer, payload []byte, chunkSize int, waitBandwidth func(context.Context, int64) error, recordBandwidth func(string, int64)) (int, error) {
	total := 0
	for len(payload) > 0 {
		chunk := payload
		if chunkSize > 0 && len(chunk) > chunkSize {
			chunk = chunk[:chunkSize]
		}
		if waitBandwidth != nil {
			if err := waitBandwidth(ctx, int64(len(chunk))); err != nil {
				return total, err
			}
		}
		n, err := w.Write(chunk)
		total += n
		if n > 0 && recordBandwidth != nil {
			recordBandwidth("out", int64(n))
		}
		if err != nil {
			return total, err
		}
		if n <= 0 {
			return total, fmt.Errorf("short write")
		}
		payload = payload[n:]
	}
	return total, nil
}

func writeAllChunked(w io.Writer, payload []byte, chunkSize int) (int, error) {
	if chunkSize <= 0 {
		chunkSize = len(payload)
	}
	total := 0
	for len(payload) > 0 {
		chunk := payload
		if len(chunk) > chunkSize {
			chunk = chunk[:chunkSize]
		}
		n, err := w.Write(chunk)
		total += n
		if err != nil {
			return total, err
		}
		if n <= 0 {
			return total, fmt.Errorf("short write")
		}
		payload = payload[n:]
	}
	return total, nil
}

func decryptHopPayload(key []byte, payload []byte) ([]byte, error) {
	if len(payload) < nonceSize {
		return nil, fmt.Errorf("payload too short")
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	return aead.Open(nil, nonce, ciphertext, nil)
}

func parseHopPayload(plaintext []byte) (bool, string, []byte, error) {
	if len(plaintext) < 1+2 {
		return false, "", nil, fmt.Errorf("plaintext too short")
	}
	isFinal := plaintext[0] == 1
	nextLen := int(binary.LittleEndian.Uint16(plaintext[1:3]))
	if len(plaintext) < 3+nextLen {
		return false, "", nil, fmt.Errorf("invalid next hop length")
	}
	nextHop := string(plaintext[3 : 3+nextLen])
	inner := plaintext[3+nextLen:]
	return isFinal, nextHop, inner, nil
}

func parseHeaderOnlyPayload(payload []byte) ([]byte, []byte, error) {
	if len(payload) < 4 {
		return nil, nil, fmt.Errorf("header-only payload too short")
	}
	headerLen := int(binary.LittleEndian.Uint32(payload[:4]))
	if headerLen <= 0 || len(payload) < 4+headerLen {
		return nil, nil, fmt.Errorf("invalid header length")
	}
	encryptedHeader := payload[4 : 4+headerLen]
	data := payload[4+headerLen:]
	return encryptedHeader, data, nil
}

func buildHeaderOnlyPayload(encryptedHeader []byte, payload []byte) []byte {
	buf := make([]byte, 4+len(encryptedHeader)+len(payload))
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(encryptedHeader)))
	copy(buf[4:], encryptedHeader)
	copy(buf[4+len(encryptedHeader):], payload)
	return buf
}

// HandleKeyExchange registers hop keys for a circuit using a Noise XX handshake.
func (h *Handler) HandleKeyExchange(stream network.Stream) {
	defer stream.Close()
	payload, err := runNoiseXXResponder(context.Background(), stream)
	if err != nil {
		return
	}
	circuitID, key, err := decodeKeyExchangePayload(payload)
	if err != nil {
		return
	}
	h.setCircuitKey(circuitID, key)
	_ = writeFrame(stream, []byte{0x01})
}

func (h *Handler) setCircuitKey(circuitID string, key []byte) {
	h.muKeys.Lock()
	defer h.muKeys.Unlock()
	h.circuitKeys[circuitID] = key
}

func (h *Handler) getCircuitKey(circuitID string) []byte {
	h.muKeys.RLock()
	defer h.muKeys.RUnlock()
	return h.circuitKeys[circuitID]
}

type rateLimitedWriter struct {
	w           io.Writer
	bytesPerSec int64
}

func (r *rateLimitedWriter) Write(p []byte) (int, error) {
	if r.bytesPerSec > 0 && int64(len(p)) > 0 {
		delay := time.Duration(int64(time.Second) * int64(len(p)) / r.bytesPerSec)
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	return r.w.Write(p)
}

// MaxCircuits returns the maximum number of concurrent circuits allowed by the handler.
func (h *Handler) MaxCircuits() int {
	return h.maxCircuits
}

// MaxBandwidth returns the maximum bandwidth allowed per circuit.
func (h *Handler) MaxBandwidth() int64 {
	return h.maxBandwidth
}

// SetMaxBandwidth updates the per-circuit bandwidth limit used by the relay handler.
func (h *Handler) SetMaxBandwidth(maxBandwidth int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.maxBandwidth = maxBandwidth
}

// ActiveCircuitCount returns the current number of active relay circuits.
func (h *Handler) ActiveCircuitCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.activeRelays)
}

// RegisterRelay manually registers an active relay circuit (primarily for testing).
func (h *Handler) RegisterRelay(circuitID string, peerID peer.ID, stream network.Stream) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.activeRelays) >= h.maxCircuits {
		return fmt.Errorf("max circuits reached")
	}

	h.activeRelays[circuitID] = &RelayInfo{
		PeerID:       peerID,
		Stream:       stream,
		CircuitID:    circuitID,
		LastActivity: time.Now(),
	}

	return nil
}

// UnregisterRelay removes a relay circuit and closes its stream.
func (h *Handler) UnregisterRelay(circuitID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if relay, ok := h.activeRelays[circuitID]; ok {
		relay.Stream.Close()
		delete(h.activeRelays, circuitID)
	}
}

// GetRelayInfo retrieves information about an active relay circuit.
func (h *Handler) GetRelayInfo(circuitID string) (*RelayInfo, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	relay, ok := h.activeRelays[circuitID]
	return relay, ok
}

// Host returns the libp2p host used by the handler.
func (h *Handler) Host() host.Host {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.host
}

// SetHost sets the libp2p host for the handler.
func (h *Handler) SetHost(host host.Host) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.host = host
}

// EnableLibp2pResourceManager toggles rcmgr-based admission and accounting.
func (h *Handler) EnableLibp2pResourceManager(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.useRCMgr = enabled
}

// SetResourceServiceName sets the rcmgr service name for stream scopes.
func (h *Handler) SetResourceServiceName(name string) {
	if name == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.serviceName = name
}

// SetBandwidthBackpressure sets a callback used to enforce bandwidth backpressure.
func (h *Handler) SetBandwidthBackpressure(fn func(context.Context, int64) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.waitBandwidth = fn
}

// SetBandwidthRecorder sets a callback used to record inbound/outbound bandwidth.
func (h *Handler) SetBandwidthRecorder(fn func(direction string, bytes int64)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recordBandwidth = fn
}

// SetUtilizationReporter sets a callback to publish active relay circuit utilization.
func (h *Handler) SetUtilizationReporter(fn func(activeCircuits int)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reportUtilization = fn
}
