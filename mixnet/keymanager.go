package mixnet

import (
	"sync"

	"github.com/libp2p/go-libp2p/mixnet/ces"
)

// KeyManager manages cryptographic key lifecycle (Issue 16).
type KeyManager struct {
	activeKeys map[string][]*ces.EncryptionKey
	mu         sync.RWMutex
}

func NewKeyManager() *KeyManager {
	return &KeyManager{
		activeKeys: make(map[string][]*ces.EncryptionKey),
	}
}

func (km *KeyManager) StoreKeys(circuitID string, keys []*ces.EncryptionKey) {
	km.mu.Lock()
	defer km.mu.Unlock()
	km.activeKeys[circuitID] = keys
}

func (km *KeyManager) GetKeys(circuitID string) ([]*ces.EncryptionKey, bool) {
	km.mu.RLock()
	defer km.mu.RUnlock()
	keys, ok := km.activeKeys[circuitID]
	return keys, ok
}

func (km *KeyManager) EraseKeys(circuitID string) {
	km.mu.Lock()
	defer km.mu.Unlock()
	if keys, ok := km.activeKeys[circuitID]; ok {
		ces.EraseKeys(keys)
		delete(km.activeKeys, circuitID)
	}
}

func (km *KeyManager) EraseAll() {
	km.mu.Lock()
	defer km.mu.Unlock()
	for _, keys := range km.activeKeys {
		ces.EraseKeys(keys)
	}
	km.activeKeys = make(map[string][]*ces.EncryptionKey)
}
