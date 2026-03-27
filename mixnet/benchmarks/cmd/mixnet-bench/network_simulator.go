package main

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	mrand "math/rand"
	"strings"
	"sync"
	"time"
)

type NetworkCondition struct {
	Name          string  `json:"name"`
	LatencyMS     int     `json:"latency_ms"`
	JitterMS      int     `json:"jitter_ms"`
	PacketLossPct float64 `json:"packet_loss_pct"`
	BandwidthMbps int     `json:"bandwidth_mbps"`
}

func (c NetworkCondition) Enabled() bool {
	return c.LatencyMS > 0 || c.JitterMS > 0 || c.PacketLossPct > 0 || c.BandwidthMbps > 0
}

func (c NetworkCondition) Summary() string {
	if !c.Enabled() {
		return "localhost (no simulated latency, jitter, loss, or bandwidth cap)"
	}
	return fmt.Sprintf("%s (%dms latency, %dms jitter, %.2f%% loss, %d Mbps)",
		c.Name, c.LatencyMS, c.JitterMS, c.PacketLossPct, c.BandwidthMbps)
}

func resolveNetworkCondition(profile string, latencyOverrideMS, jitterOverrideMS int, packetLossOverridePct float64, bandwidthOverrideMbps int) (NetworkCondition, error) {
	normalizedProfile := strings.ToLower(strings.TrimSpace(profile))
	if normalizedProfile == "" {
		normalizedProfile = "localhost"
	}

	var condition NetworkCondition
	switch normalizedProfile {
	case "localhost":
		condition = NetworkCondition{Name: "localhost"}
	case "3g":
		condition = NetworkCondition{Name: "3g", LatencyMS: 200, JitterMS: 10, PacketLossPct: 1.0, BandwidthMbps: 2}
	case "satellite":
		condition = NetworkCondition{Name: "satellite", LatencyMS: 600, JitterMS: 20, PacketLossPct: 0.5, BandwidthMbps: 25}
	case "high-latency":
		condition = NetworkCondition{Name: "high-latency", LatencyMS: 100, JitterMS: 50, PacketLossPct: 2.0, BandwidthMbps: 10}
	case "custom":
		condition = NetworkCondition{Name: "custom"}
	default:
		return NetworkCondition{}, fmt.Errorf("unknown network profile %q", profile)
	}

	if latencyOverrideMS >= 0 {
		condition.LatencyMS = latencyOverrideMS
	}
	if jitterOverrideMS >= 0 {
		condition.JitterMS = jitterOverrideMS
	}
	if packetLossOverridePct >= 0 {
		condition.PacketLossPct = packetLossOverridePct
	}
	if bandwidthOverrideMbps >= 0 {
		condition.BandwidthMbps = bandwidthOverrideMbps
	}

	if condition.LatencyMS < 0 {
		return NetworkCondition{}, fmt.Errorf("network latency must be >= 0")
	}
	if condition.JitterMS < 0 {
		return NetworkCondition{}, fmt.Errorf("network jitter must be >= 0")
	}
	if condition.PacketLossPct < 0 || condition.PacketLossPct >= 100 {
		return NetworkCondition{}, fmt.Errorf("network packet loss must be between 0 and less than 100, got %.2f", condition.PacketLossPct)
	}
	if condition.BandwidthMbps < 0 {
		return NetworkCondition{}, fmt.Errorf("network bandwidth must be >= 0")
	}

	return condition, nil
}

type networkSimulator struct {
	condition NetworkCondition
	rng       randomSource
	mu        sync.Mutex
}

type randomSource interface {
	Intn(int) int
	Float64() float64
}

func newNetworkSimulator(condition NetworkCondition) *networkSimulator {
	return &networkSimulator{
		condition: condition,
		rng:       newBenchmarkRand(),
	}
}

func newBenchmarkRand() randomSource {
	var seed int64
	if err := binary.Read(crand.Reader, binary.LittleEndian, &seed); err != nil {
		seed = time.Now().UnixNano()
	}
	return mrand.New(mrand.NewSource(seed))
}

func (s *networkSimulator) connect(ctx context.Context, stages int) error {
	if s == nil || !s.condition.Enabled() {
		return nil
	}
	if stages < 1 {
		stages = 1
	}
	for i := 0; i < stages; i++ {
		if err := s.delay(ctx, s.sampleLatency()); err != nil {
			return err
		}
	}
	return nil
}

func (s *networkSimulator) transmit(ctx context.Context, bytes int, writeFn func() error) error {
	if s == nil || !s.condition.Enabled() {
		return writeFn()
	}
	for {
		if err := s.delay(ctx, s.sampleTransferDelay(bytes)); err != nil {
			return err
		}
		if !s.shouldDrop() {
			return writeFn()
		}
	}
}

func (s *networkSimulator) delay(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *networkSimulator) sampleLatency() time.Duration {
	if s == nil {
		return 0
	}
	totalMS := s.condition.LatencyMS
	if s.condition.JitterMS > 0 {
		s.mu.Lock()
		jitter := s.rng.Intn(2*s.condition.JitterMS+1) - s.condition.JitterMS
		s.mu.Unlock()
		totalMS += jitter
	}
	if totalMS < 0 {
		totalMS = 0
	}
	return time.Duration(totalMS) * time.Millisecond
}

func (s *networkSimulator) sampleTransferDelay(bytes int) time.Duration {
	delay := s.sampleLatency()
	if s == nil || s.condition.BandwidthMbps <= 0 || bytes <= 0 {
		return delay
	}
	bitsPerSecond := float64(s.condition.BandwidthMbps) * 1_000_000
	throttleDelay := time.Duration((float64(bytes) * 8 * float64(time.Second)) / bitsPerSecond)
	return delay + throttleDelay
}

func (s *networkSimulator) shouldDrop() bool {
	if s == nil || s.condition.PacketLossPct <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rng.Float64()*100 < s.condition.PacketLossPct
}
