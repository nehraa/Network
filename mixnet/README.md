# Lib-Mix package guide

`mixnet/` contains the implementation of the Lib-Mix protocol for `go-libp2p`.
The package is designed to feel like other libp2p transports and protocol
adapters: the public API is centered around configuration, construction, and a
stream-like interface, while the lower-level packages handle relay discovery,
circuit lifecycle, and packet processing.

## What the package does

Lib-Mix adds metadata privacy to libp2p streams by routing traffic through
multiple relays and, when enabled, splitting a payload into several shards that
travel over independent circuits.

At a high level:

1. `NewMixnet` validates configuration and initializes the supporting
   subsystems.
2. `EstablishConnection`, `OpenStream`, or `Send` discovers relays and builds
   parallel circuits to the destination.
3. Payload data is optionally compressed, encrypted, padded, and sharded.
4. Relays forward only what they need to know for the next hop.
5. The destination reconstructs the payload and exposes it through
   `AcceptStream` or the destination-side session channel.

## Directory structure

| Path | Purpose |
| --- | --- |
| `config.go` | Mixnet configuration, defaults, validation, and derived values. |
| `upgrader.go` | Core `Mixnet` type and end-to-end send/receive orchestration. |
| `stream.go` | Stream-oriented API (`OpenStream`, `AcceptStream`, `Read`, `Write`, `Close`). |
| `privacy_transport.go` | Privacy packet format used between origin, relays, and destination. |
| `onion.go` / `onion_header.go` | Onion header construction, layered encryption, and header parsing. |
| `padding.go` / `auth_tag.go` | Size-hiding padding and optional shard authenticity tags. |
| `session_crypto.go` / `noise_key_exchange.go` / `key_management.go` | Session payload encryption and Noise-based key exchange helpers. |
| `relay_discovery.go` / `discovery/` | Relay discovery, sampling, and selection. |
| `circuit/` | Circuit state, circuit construction, heartbeats, and recovery. |
| `relay/` | Relay-side forwarding handlers and relay key exchange. |
| `ces/` | Optional Compress-Encrypt-Shard pipeline implementation. |
| `metrics*.go` / `resource_management.go` / `failure_detection.go` | Operations, observability, and runtime protections. |
| `Docs/` | Narrative protocol, configuration, and component documentation. |

## Documentation map

The markdown documentation in `mixnet/Docs` is split into two groups:

- `Docs/README/`: implementation-facing guides for the main package and
  subpackages.
- `Docs/PRD/`: design, requirement, and configuration documents aligned to the
  current implementation.

Useful starting points:

- `Docs/README/mixnet-readme.md`: protocol overview and usage.
- `Docs/README/project-structure.md`: file-by-file map of the mixnet folder.
- `Docs/PRD/design.md`: end-to-end design and protocol walkthrough.
- `Docs/PRD/configuration-reference.md`: configuration defaults and trade-offs.

## Public API entry points

- `DefaultConfig` / `NewMixnetConfig`: create configuration.
- `NewMixnet`: construct the runtime.
- `OpenStream`: create an outbound stream-like session to a destination peer.
- `AcceptStream`: wait for an inbound session.
- `Send`: send a payload without manually managing a stream wrapper.

For lower-level details, consult the Go doc comments in the package and the
supporting documents linked above.
