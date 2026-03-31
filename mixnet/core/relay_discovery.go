// Package mixnet provides discovery wrappers and traffic-shaping helpers.
package mixnet

import (
	"context"
	crand "crypto/rand"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/routing"

	"github.com/ipfs/go-cid"
	mh "github.com/multiformats/go-multihash"

	"github.com/libp2p/go-libp2p/mixnet/circuit"
	"github.com/libp2p/go-libp2p/mixnet/discovery"
)

// DiscoverRelaysWithVerification discovers relay providers and delegates filtering/selection
// to the canonical discovery layer in mixnet/discovery.
func DiscoverRelaysWithVerification(ctx context.Context, h host.Host, r routing.Routing, dest peer.ID, protoID string, hopCount, circuitCount int, samplingSize int, selectionMode string, randomnessFactor float64) ([]circuit.RelayInfo, error) {
	// Create CID for discovery (same model used by upgrader).
	hHash, err := mh.Encode([]byte(protoID+"-relay-v1"), mh.SHA2_256)
	if err != nil {
		return nil, ErrDiscoveryFailed("failed to encode discovery CID").WithCause(err)
	}
	c := cid.NewCidV1(cid.Raw, hHash)

	providersChan := r.FindProvidersAsync(ctx, c, 0)
	var providers []peer.AddrInfo
	for p := range providersChan {
		providers = append(providers, p)
	}

	// Canonical exclusion logic lives in discovery.FilterByExclusion.
	providers = discovery.FilterByExclusion(providers, dest, h.ID())
	if len(providers) == 0 {
		return nil, ErrDiscoveryFailed("no relay providers after exclusion")
	}

	disc := discovery.NewRelayDiscoveryWithHost(h, protoID, samplingSize, selectionMode, randomnessFactor)
	sel, err := disc.FindRelays(ctx, providers, hopCount, circuitCount)
	if err != nil {
		return nil, ErrDiscoveryFailed("relay selection failed").WithCause(err)
	}

	out := make([]circuit.RelayInfo, len(sel))
	for i, ri := range sel {
		out[i] = circuit.RelayInfo{PeerID: ri.PeerID, AddrInfo: ri.AddrInfo, Latency: ri.Latency, Connected: ri.Available}
	}
	return out, nil
}

// UseDiscoveryService returns the canonical discovery service implementation.
func UseDiscoveryService(h host.Host, protoID string, samplingSize int, selectionMode string, randomnessFactor float64) (*discovery.RelayDiscovery, error) {
	disc := discovery.NewRelayDiscoveryWithHost(h, protoID, samplingSize, selectionMode, randomnessFactor)
	return disc, nil
}

// ============================================================
// Cover Traffic - Padding and Timing Obfuscation
// ============================================================

// CoverTrafficConfig holds cover traffic configuration.
type CoverTrafficConfig struct {
	Enabled    bool
	Interval   time.Duration
	PacketSize int
	Jitter     time.Duration
}

// DefaultCoverTrafficConfig returns sensible defaults.
func DefaultCoverTrafficConfig() *CoverTrafficConfig {
	return &CoverTrafficConfig{
		Enabled:    true,
		Interval:   1 * time.Second,
		PacketSize: 1024,
		Jitter:     500 * time.Millisecond,
	}
}

// CoverTrafficGenerator generates cover traffic to prevent timing analysis.
type CoverTrafficGenerator struct {
	config       *CoverTrafficConfig
	stopCh       chan struct{}
	stopOnce     sync.Once
	wg           sync.WaitGroup
	senderMu     sync.RWMutex
	sender       func(context.Context, peer.ID, []byte) error
	errorHandler func(error)
	rngMu        sync.Mutex
	rng          *rand.Rand
}

// NewCoverTrafficGenerator creates a new cover traffic generator.
func NewCoverTrafficGenerator(cfg *CoverTrafficConfig) *CoverTrafficGenerator {
	if cfg == nil {
		cfg = DefaultCoverTrafficConfig()
	}
	return &CoverTrafficGenerator{
		config: cfg,
		stopCh: make(chan struct{}),
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// SetSender wires the transport hook used to emit cover traffic packets.
func (ctg *CoverTrafficGenerator) SetSender(sender func(context.Context, peer.ID, []byte) error) {
	ctg.senderMu.Lock()
	defer ctg.senderMu.Unlock()
	ctg.sender = sender
}

// SetErrorHandler receives asynchronous delivery errors from the background loop.
func (ctg *CoverTrafficGenerator) SetErrorHandler(fn func(error)) {
	ctg.senderMu.Lock()
	defer ctg.senderMu.Unlock()
	ctg.errorHandler = fn
}

// Start begins generating cover traffic to the discovered peer set.
func (ctg *CoverTrafficGenerator) Start(ctx context.Context, getPeers func() []peer.ID) {
	if !ctg.config.Enabled {
		return
	}

	ctg.wg.Add(1)
	go func() {
		defer ctg.wg.Done()

		for {
			wait := ctg.nextInterval()
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-ctg.stopCh:
				timer.Stop()
				return
			case <-timer.C:
				peers := getPeers()
				if len(peers) > 0 {
					if err := ctg.sendCoverTraffic(ctx, peers); err != nil {
						ctg.handleError(err)
					}
				}
			}
		}
	}()
}

// Stop stops cover traffic generation.
func (ctg *CoverTrafficGenerator) Stop() {
	ctg.stopOnce.Do(func() {
		close(ctg.stopCh)
	})
	ctg.wg.Wait()
}

func (ctg *CoverTrafficGenerator) nextInterval() time.Duration {
	interval := ctg.config.Interval
	if interval <= 0 {
		interval = time.Second
	}
	if ctg.config.Jitter <= 0 {
		return interval
	}
	jitterWindow := int64(ctg.config.Jitter)*2 + 1
	delta := time.Duration(ctg.randomInt63n(jitterWindow)) - ctg.config.Jitter
	if interval+delta < time.Millisecond {
		return time.Millisecond
	}
	return interval + delta
}

func (ctg *CoverTrafficGenerator) handleError(err error) {
	if err == nil {
		return
	}
	ctg.senderMu.RLock()
	handler := ctg.errorHandler
	ctg.senderMu.RUnlock()
	if handler != nil {
		handler(err)
	}
}

// sendCoverTraffic emits one cover packet to a randomly selected peer.
func (ctg *CoverTrafficGenerator) sendCoverTraffic(ctx context.Context, peers []peer.ID) error {
	if len(peers) == 0 {
		return nil
	}
	if ctg.config.PacketSize <= 0 {
		return errors.New("cover traffic packet size must be positive")
	}

	ctg.senderMu.RLock()
	sender := ctg.sender
	ctg.senderMu.RUnlock()
	if sender == nil {
		return errors.New("cover traffic sender is not configured")
	}

	payload := make([]byte, ctg.config.PacketSize)
	if _, err := crand.Read(payload); err != nil {
		return err
	}
	target := peers[ctg.randomIntn(len(peers))]
	return sender(ctx, target, payload)
}

func (ctg *CoverTrafficGenerator) randomIntn(n int) int {
	if n <= 1 {
		return 0
	}
	ctg.rngMu.Lock()
	defer ctg.rngMu.Unlock()
	return ctg.rng.Intn(n)
}

func (ctg *CoverTrafficGenerator) randomInt63n(n int64) int64 {
	if n <= 1 {
		return 0
	}
	ctg.rngMu.Lock()
	defer ctg.rngMu.Unlock()
	return ctg.rng.Int63n(n)
}
