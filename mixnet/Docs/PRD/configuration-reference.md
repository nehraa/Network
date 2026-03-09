# Configuration Reference (Implementation)

This document provides a complete reference for all configuration options in the Go implementation, including those not present in the original design.

## Core Configuration (From Original Design)

### `HopCount`
- **Type**: `int`
- **Range**: 1-10
- **Default**: 2
- **Description**: Number of relay nodes in each circuit
- **Privacy Impact**: Higher = more privacy, higher latency
- **Requirement**: Req 1

### `CircuitCount`
- **Type**: `int`
- **Range**: 1-20
- **Default**: 3
- **Description**: Number of parallel circuits to establish
- **Privacy Impact**: Higher = more redundancy, more bandwidth
- **Requirement**: Req 2

### `Compression`
- **Type**: `string`
- **Values**: `"gzip"`, `"snappy"`
- **Default**: `"snappy"`
- **Description**: Compression algorithm for CES pipeline
- **Performance Impact**: Gzip = better compression, Snappy = lower latency
- **Requirement**: Req 3

### `ErasureThreshold`
- **Type**: `int`
- **Range**: 1 to `CircuitCount`
- **Default**: `ceil(CircuitCount * 0.6)`
- **Description**: Minimum shards needed to reconstruct data
- **Reliability Impact**: Lower = faster reconstruction, less redundancy
- **Requirement**: Req 3

### `SelectionMode`
- **Type**: `SelectionMode` (enum)
- **Values**: `"rtt"`, `"random"`, `"hybrid"`
- **Default**: `"rtt"`
- **Description**: Relay selection strategy
- **Privacy Impact**: 
  - `rtt`: Best performance, predictable (lower privacy)
  - `random`: Unpredictable (higher privacy), variable performance
  - `hybrid`: Balanced
- **Requirement**: Req 4, Req 5

### `SamplingSize`
- **Type**: `int`
- **Default**: `3 * (HopCount * CircuitCount)`
- **Description**: Number of relay candidates to sample in hybrid mode
- **Performance Impact**: Higher = more DHT queries, better relay quality
- **Requirement**: Req 4

### `RandomnessFactor`
- **Type**: `float64`
- **Range**: 0.0-1.0
- **Default**: 0.3
- **Description**: Weight of randomness vs RTT in hybrid selection
- **Privacy Impact**: Higher = more random (higher privacy), variable performance
- **Requirement**: Req 5

---

## Implementation-Specific Configuration

### `UseCESPipeline`
- **Type**: `bool`
- **Default**: `true`
- **Description**: Enable/disable Compress-Encrypt-Shard pipeline
- **When to Disable**: 
  - Small messages (<1KB)
  - Latency-critical applications
  - Don't need redundancy
- **Impact When Disabled**:
  - ✅ Lower latency
  - ✅ Lower CPU usage
  - ❌ No compression
  - ❌ No redundancy (all circuits must succeed)
- **Requirement**: Req 21

### `EncryptionMode`
- **Type**: `EncryptionMode` (enum)
- **Values**: `"full"`, `"header-only"`
- **Default**: `"full"`
- **Description**: Encryption strategy
- **When to Use Header-Only**:
  - Large payloads (>16KB)
  - CPU-constrained relays
  - Acceptable to encrypt payload once end-to-end
- **Performance Impact**: Header-only is 2-5× faster for large payloads
- **Requirement**: Req 3A

---

## Padding Configuration

### `HeaderPaddingEnabled`
- **Type**: `bool`
- **Default**: `false`
- **Description**: Enable random padding in privacy headers
- **When to Enable**: High-privacy deployments
- **Privacy Impact**: Prevents relay fingerprinting by header size
- **Bandwidth Impact**: +0-256 bytes per shard
- **Requirement**: Req 22

### `HeaderPaddingMin`
- **Type**: `int`
- **Default**: 0
- **Description**: Minimum header padding in bytes
- **Recommendation**: Set to 0 for efficiency

### `HeaderPaddingMax`
- **Type**: `int`
- **Default**: 256
- **Description**: Maximum header padding in bytes
- **Recommendation**: 256 bytes is sufficient for most use cases

### `PayloadPaddingStrategy`
- **Type**: `PaddingStrategy` (enum)
- **Values**: `"none"`, `"random"`, `"buckets"`
- **Default**: `"none"`
- **Description**: Payload length padding strategy
- **When to Use**:
  - `none`: Low-privacy deployments, bandwidth-constrained
  - `random`: High-privacy, variable message sizes
  - `buckets`: High-privacy, common message sizes
- **Requirement**: Req 23

### `PayloadPaddingMin`
- **Type**: `int`
- **Default**: 0
- **Description**: Minimum random padding in bytes (for `random` strategy)
- **Recommendation**: Set to 10-20% of average message size

### `PayloadPaddingMax`
- **Type**: `int`
- **Default**: 1024
- **Description**: Maximum random padding in bytes (for `random` strategy)
- **Recommendation**: Set to 50-100% of average message size

### `PayloadPaddingBuckets`
- **Type**: `[]int`
- **Default**: `[1024, 4096, 16384, 65536]` (1KB, 4KB, 16KB, 64KB)
- **Description**: Target sizes for bucket padding
- **Recommendation**: Choose buckets based on your message size distribution
- **Example**: For chat app: `[256, 1024, 4096]` (short, medium, long messages)

---

## Integrity Configuration

### `EnableAuthTag`
- **Type**: `bool`
- **Default**: `false`
- **Description**: Enable per-shard authenticity tags
- **When to Enable**:
  - Untrusted relay networks
  - Need early corruption detection
  - Debugging integrity issues
- **Performance Impact**: +16 bytes per shard, +HMAC computation
- **Requirement**: Req 24

### `AuthTagSize`
- **Type**: `int`
- **Default**: 16
- **Description**: Size of truncated HMAC tag in bytes
- **Recommendation**: 16 bytes (128 bits) is sufficient
- **Security**: Don't go below 12 bytes (96 bits)

---

## Timing Configuration

### `MaxJitter`
- **Type**: `int` (milliseconds)
- **Default**: 0 (disabled)
- **Description**: Maximum random delay between shard transmissions
- **When to Enable**: High-privacy deployments
- **Privacy Impact**: Breaks timing correlations between shards
- **Latency Impact**: Adds 0-`MaxJitter` ms per shard
- **Recommendation**: 
  - Low-latency apps: 0-10ms
  - Balanced: 10-50ms
  - High-privacy: 50-200ms
- **Requirement**: Req 25

---

## Relay Configuration

### `MaxCircuits`
- **Type**: `int`
- **Default**: 100
- **Description**: Maximum concurrent circuits for relay nodes
- **When to Adjust**:
  - Increase for high-capacity relays
  - Decrease for resource-constrained nodes
- **Requirement**: Req 20, Req 26

### `MaxBandwidth`
- **Type**: `int64` (bytes/sec)
- **Default**: 10485760 (10 MB/s)
- **Description**: Maximum bandwidth per circuit
- **When to Adjust**:
  - Increase for high-bandwidth relays
  - Decrease to prevent abuse
- **Requirement**: Req 20, Req 26

---

## Timeout Configuration

### `ConstructionTimeout`
- **Type**: `time.Duration`
- **Default**: 30 seconds
- **Description**: Maximum time to establish all circuits
- **When to Adjust**:
  - Increase for slow networks
  - Decrease for fast networks

### `HealthCheckInterval`
- **Type**: `time.Duration`
- **Default**: 10 seconds
- **Description**: Interval between circuit health checks
- **When to Adjust**:
  - Decrease for faster failure detection
  - Increase to reduce overhead

### `ShardReceptionTimeout`
- **Type**: `time.Duration`
- **Default**: 30 seconds
- **Description**: Maximum time to wait for shard reconstruction
- **When to Adjust**:
  - Increase for slow networks
  - Decrease for fast networks

### `GracefulShutdownTimeout`
- **Type**: `time.Duration`
- **Default**: 10 seconds
- **Description**: Maximum time to wait for close acknowledgments
- **When to Adjust**:
  - Increase for slow networks
  - Decrease for fast shutdown

---

## Configuration Presets

### Low-Latency Preset
```go
config := &MixnetConfig{
    HopCount:               1,
    CircuitCount:           2,
    Compression:            "snappy",
    ErasureThreshold:       1,
    UseCESPipeline:         false,
    EncryptionMode:         "header-only",
    SelectionMode:          "rtt",
    PayloadPaddingStrategy: "none",
    MaxJitter:              0,
}
```
**Use Case**: Real-time chat, gaming, VoIP

### Balanced Preset (Default)
```go
config := &MixnetConfig{
    HopCount:               2,
    CircuitCount:           3,
    Compression:            "snappy",
    ErasureThreshold:       2,
    UseCESPipeline:         true,
    EncryptionMode:         "full",
    SelectionMode:          "rtt",
    PayloadPaddingStrategy: "none",
    MaxJitter:              0,
}
```
**Use Case**: General-purpose applications

### High-Privacy Preset
```go
config := &MixnetConfig{
    HopCount:               3,
    CircuitCount:           5,
    Compression:            "gzip",
    ErasureThreshold:       3,
    UseCESPipeline:         true,
    EncryptionMode:         "full",
    SelectionMode:          "hybrid",
    RandomnessFactor:       0.5,
    HeaderPaddingEnabled:   true,
    HeaderPaddingMax:       256,
    PayloadPaddingStrategy: "buckets",
    PayloadPaddingBuckets:  []int{1024, 4096, 16384, 65536},
    EnableAuthTag:          true,
    AuthTagSize:            16,
    MaxJitter:              100,
}
```
**Use Case**: Whistleblowing, journalism, activism

### High-Throughput Preset
```go
config := &MixnetConfig{
    HopCount:               2,
    CircuitCount:           5,
    Compression:            "snappy",
    ErasureThreshold:       3,
    UseCESPipeline:         true,
    EncryptionMode:         "header-only",
    SelectionMode:          "rtt",
    PayloadPaddingStrategy: "none",
    MaxJitter:              0,
}
```
**Use Case**: File transfer, video streaming

---

## Configuration Validation Rules

The implementation validates configuration at initialization:

1. **HopCount**: Must be 1-10
2. **CircuitCount**: Must be 1-20
3. **ErasureThreshold**: Must be 1 to `CircuitCount`
4. **RandomnessFactor**: Must be 0.0-1.0
5. **HeaderPaddingMin**: Must be ≤ `HeaderPaddingMax`
6. **PayloadPaddingMin**: Must be ≤ `PayloadPaddingMax`
7. **PayloadPaddingBuckets**: Must be in ascending order
8. **AuthTagSize**: Must be 12-32 bytes
9. **MaxJitter**: Must be ≥ 0

Invalid configurations return an error immediately.

---

## Performance vs Privacy Trade-offs

| Configuration | Latency | Bandwidth | Privacy | Reliability |
|---------------|---------|-----------|---------|-------------|
| Low-Latency | ✅ Best | ✅ Best | ❌ Lowest | ❌ Lowest |
| Balanced | ✅ Good | ✅ Good | ✅ Good | ✅ Good |
| High-Privacy | ❌ Worst | ❌ Worst | ✅ Best | ✅ Best |
| High-Throughput | ✅ Good | ❌ High | ✅ Medium | ✅ Best |

---

## Monitoring Configuration Impact

Use the MetricsCollector to monitor how configuration affects performance:

```go
metrics := mixnet.GetMetrics()
fmt.Printf("Avg Circuit RTT: %v\n", metrics.AvgCircuitRTT)
fmt.Printf("Compression Ratio: %.2f\n", metrics.CompressionRatio)
fmt.Printf("Circuit Success Rate: %.2f%%\n", metrics.ConstructionSuccessRate * 100)
```

Adjust configuration based on metrics:
- High RTT → Reduce `HopCount` or use `SelectionMode: "rtt"`
- Low compression ratio → Disable `UseCESPipeline` for small messages
- High failure rate → Increase `ErasureThreshold` or `CircuitCount`

---

## Configuration Best Practices

1. **Start with defaults**: The balanced preset works for most use cases
2. **Measure first**: Use metrics to identify bottlenecks before tuning
3. **Tune incrementally**: Change one parameter at a time
4. **Test in production**: Synthetic benchmarks don't capture real-world behavior
5. **Document choices**: Record why you chose specific values
6. **Monitor continuously**: Configuration needs change as usage patterns evolve

---

## Future Configuration Options

Potential additions for future versions:

- **Adaptive Configuration**: Automatically adjust based on network conditions
- **Per-Destination Config**: Different settings for different peers
- **Traffic Shaping**: Rate limiting, burst control
- **Advanced Padding**: Cover traffic, dummy messages
- **Circuit Pooling**: Reuse circuits across streams
- **Multi-Path Routing**: Use different paths for different shards

These are not yet implemented but may be added based on user feedback and research.
