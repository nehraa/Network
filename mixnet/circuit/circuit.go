package circuit

import (
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

// CircuitState describes the lifecycle state of a circuit managed by the
// circuit package.
type CircuitState int

const (
	// StatePending marks a circuit that has been allocated but not yet built.
	StatePending CircuitState = iota
	// StateBuilding marks a circuit that is currently being established.
	StateBuilding
	// StateActive marks a circuit that is ready to carry mixnet traffic.
	StateActive
	// StateFailed marks a circuit that can no longer be used successfully.
	StateFailed
	// StateClosed marks a circuit that has been intentionally torn down.
	StateClosed
)

// String returns a stable human-readable name for the circuit state.
func (s CircuitState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateBuilding:
		return "building"
	case StateActive:
		return "active"
	case StateFailed:
		return "failed"
	case StateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// Circuit represents one ordered relay path from an origin to a destination.
type Circuit struct {
	// ID is the local identifier used to track the circuit inside the runtime.
	ID string
	// Peers stores the ordered relay path, from entry relay to exit relay.
	Peers []peer.ID
	// State tracks where the circuit is in its lifecycle.
	State CircuitState
	// CreatedAt records when the circuit object was allocated.
	CreatedAt time.Time
	// UpdatedAt records the last state or heartbeat update.
	UpdatedAt time.Time
	// FailureCount counts how many times the circuit has been marked failed.
	FailureCount int
	// LastHeartbeat records the most recent successful heartbeat timestamp.
	LastHeartbeat time.Time
	mu            sync.RWMutex
}

// NewCircuit allocates a circuit with the provided ID and relay order.
func NewCircuit(id string, peers []peer.ID) *Circuit {
	return &Circuit{
		ID:        id,
		Peers:     peers,
		State:     StatePending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// GetState returns the circuit's current lifecycle state.
func (c *Circuit) GetState() CircuitState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.State
}

// SetState updates the circuit lifecycle state and refreshes UpdatedAt.
func (c *Circuit) SetState(state CircuitState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.State = state
	c.UpdatedAt = time.Now()
}

// MarkFailed moves the circuit into the failed state and increments the failure
// counter used by recovery logic.
func (c *Circuit) MarkFailed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.State = StateFailed
	c.FailureCount++
	c.UpdatedAt = time.Now()
}

// IsActive reports whether the circuit is currently available for traffic.
func (c *Circuit) IsActive() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.State == StateActive
}

// GetLastHeartbeat returns the timestamp of the most recent heartbeat observed
// for the circuit.
func (c *Circuit) GetLastHeartbeat() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LastHeartbeat
}

// SetLastHeartbeat stores ts as the most recent heartbeat and refreshes
// UpdatedAt.
func (c *Circuit) SetLastHeartbeat(ts time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LastHeartbeat = ts
	c.UpdatedAt = time.Now()
}

// Entry returns the first relay in the circuit path.
func (c *Circuit) Entry() peer.ID {
	if len(c.Peers) == 0 {
		return ""
	}
	return c.Peers[0]
}

// Exit returns the final relay in the circuit path.
func (c *Circuit) Exit() peer.ID {
	if len(c.Peers) == 0 {
		return ""
	}
	return c.Peers[len(c.Peers)-1]
}

// FailureEvent represents a circuit failure event for active failure detection.
type FailureEvent struct {
	CircuitID  string
	PeerID     peer.ID
	RemoteAddr string
	Timestamp  time.Time
}
