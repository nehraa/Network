# Design Document: Lib-Mix Protocol (Requirements 1-10)

## Overview

Lib-Mix is a high-performance metadata-private communication protocol for libp2p that combines configurable onion routing with multi-path sharding.

## Core Components

### 1. Configuration (Req 1, 2, 15)

```go
type MixnetConfig struct {
    HopCount         int     // 1-10, default 2
    CircuitCount     int     // 1-20, default 3
    Compression      string  // "gzip" | "snappy", default "gzip"
    ErasureThreshold int     // must be < CircuitCount

    // Relay selection (Req 4)
    SelectionMode    string  // "rtt" | "random" | "hybrid", default "rtt"
    SamplingSize     int     // K candidates to sample, default 3 * hopCount * circuitCount
    RandomnessFactor float64 // [0.0, 1.0], default 0.3
}
```

### 2. CES Pipeline (Req 3)

- **Compress**: Gzip or Snappy based on config
- **Encrypt**: Layered Noise encryption (one layer per hop)
- **Shard**: Reed-Solomon encoding to generate N shards

### 3. Relay Discovery (Req 4, 5)

**Algorithm: Configurable Selection Modes**

**Selection Modes (Req 4.6):**
- `rtt`: Sort by RTT ascending, pick lowest latency
- `random`: Pure random selection from filtered pool
- `hybrid` (default): Random sample + weighted selection

**Hybrid Mode Algorithm:**
1. **Random Sample K**: From DHT pool (3x required), randomly sample K = `sampling_size` candidates
2. **Measure RTT**: Measure latency to each sampled candidate (5s timeout per Req 5)
3. **Weighted Selection**: For each circuit position, select using:
   - Weight formula: `w = (1/RTT) * (1 - randomness_factor) + (random() * randomness_factor)`
   - Higher randomness_factor → more diversity, higher latency
   - Lower randomness_factor → lower latency, less diversity

**Privacy**: Hybrid mode avoids deterministic RTT sorting that could fingerprint users

### 4. Circuit Manager (Req 6, 8, 10)

- Construct N independent circuits to destination
- Each circuit has unique relay chain
- Assign one shard per circuit
- Transmit shards in parallel
- **Failure Recovery**: detect relay failure within 5s, rebuild circuit from pool, continue if threshold met

### 5. Relay Forwarding (Req 7)

- Decrypt outer layer only
- Read next-hop from header
- Forward to next peer
- Zero-knowledge (no logging, no inspection)

### 6. Destination Reception (Req 9)

- Buffer shards until threshold met (30s timeout)
- Reconstruct via Reed-Solomon
- Decrypt layers in reverse
- Decompress and deliver

## Package Structure

```
mixnet/
├── config.go           # MixnetConfig, validation
├── upgrader.go        # StreamUpgrader implementation
├── ces/
│   ├── pipeline.go    # CES pipeline orchestration
│   ├── compression.go # Gzip/Snappy
│   ├── encryption.go  # Layered Noise
│   └── sharding.go    # Reed-Solomon
├── circuit/
│   ├── manager.go     # Circuit lifecycle + failure recovery
│   ├── circuit.go     # Circuit representation
│   └── keys.go        # Ephemeral key management
├── discovery/
│   ├── dht.go         # DHT relay discovery
│   └── latency.go      # RTT measurement + weighted selection
└── relay/
    ├── handler.go      # Relay stream handler
    └── forward.go      # Forwarding logic
```
