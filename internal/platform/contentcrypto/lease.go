package contentcrypto

import "sync"

// EphemeralKeyLease 在contentcrypto包内短暂持有raw key；Close会覆写并失效。
type EphemeralKeyLease struct {
	mu       sync.Mutex
	metadata KeyMetadata
	key      []byte
	closed   bool
}

// NewEphemeralKeyLease 接受key provider的32-byte AES key并立即复制。
func NewEphemeralKeyLease(metadata KeyMetadata, key []byte) (*EphemeralKeyLease, error) {
	if err := metadata.validate(); err != nil || len(key) != AES256KeyBytes {
		return nil, ErrKeyLeaseInvalid
	}
	metadata.Nonce = append([]byte(nil), metadata.Nonce...)
	return &EphemeralKeyLease{metadata: metadata, key: append([]byte(nil), key...)}, nil
}

func (l *EphemeralKeyLease) snapshot() (KeyMetadata, []byte, error) {
	if l == nil {
		return KeyMetadata{}, nil, ErrKeyLeaseInvalid
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || len(l.key) != AES256KeyBytes {
		return KeyMetadata{}, nil, ErrKeyLeaseInvalid
	}
	metadata := l.metadata
	metadata.Nonce = append([]byte(nil), l.metadata.Nonce...)
	return metadata, append([]byte(nil), l.key...), nil
}

// Close 覆写lease内的raw key；重复Close保持幂等。
func (l *EphemeralKeyLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	clear(l.key)
	l.key = nil
	l.closed = true
	return nil
}
