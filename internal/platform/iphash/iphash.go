// Package iphash turns client IPs into stored, non-reversible hashes. Raw IPs
// are never persisted or logged.
package iphash

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/netip"

	"github.com/sky/blog-service/internal/domain"
)

// Hasher computes HMAC-SHA256(secret, canonical IP) truncated to 16 bytes.
// It is safe for concurrent use.
type Hasher struct {
	secret []byte
}

// New requires a non-empty secret: without one the hash of the small IPv4
// space could be brute-forced back to IPs.
func New(secret []byte) (*Hasher, error) {
	if len(secret) == 0 {
		return nil, errors.New("iphash: secret must not be empty")
	}
	return &Hasher{secret: append([]byte(nil), secret...)}, nil
}

// Hash canonicalizes the IP (IPv4-mapped IPv6 becomes IPv4, zone dropped) so
// the same client always maps to the same key.
func (h *Hasher) Hash(ip netip.Addr) domain.IPHash {
	canonical := ip.WithZone("").Unmap().AsSlice() // 4 bytes for IPv4, 16 for IPv6
	mac := hmac.New(sha256.New, h.secret)
	mac.Write(canonical)
	var out domain.IPHash
	copy(out[:], mac.Sum(nil)[:domain.IPHashSize])
	return out
}
