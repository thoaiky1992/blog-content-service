package iphash

import (
	"bytes"
	"net/netip"
	"testing"
)

func mustNew(t *testing.T, secret string) *Hasher {
	t.Helper()
	h, err := New([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestDeterministicAndSized(t *testing.T) {
	h := mustNew(t, "s3cret")
	ip := netip.MustParseAddr("203.0.113.9")
	a, b := h.Hash(ip), h.Hash(ip)
	if a != b {
		t.Error("hash must be deterministic")
	}
	if len(a) != 16 || a == ([16]byte{}) {
		t.Errorf("bad hash: %x", a)
	}
}

func TestDifferentIPsAndSecretsDiffer(t *testing.T) {
	h := mustNew(t, "s3cret")
	if h.Hash(netip.MustParseAddr("1.1.1.1")) == h.Hash(netip.MustParseAddr("1.1.1.2")) {
		t.Error("different IPs must hash differently")
	}
	if h.Hash(netip.MustParseAddr("1.1.1.1")) == mustNew(t, "other").Hash(netip.MustParseAddr("1.1.1.1")) {
		t.Error("different secrets must hash differently")
	}
}

func TestCanonicalization(t *testing.T) {
	h := mustNew(t, "s3cret")
	v4 := h.Hash(netip.MustParseAddr("198.51.100.7"))
	if v4 != h.Hash(netip.MustParseAddr("::ffff:198.51.100.7")) {
		t.Error("IPv4-mapped IPv6 must equal the IPv4 hash")
	}
	if h.Hash(netip.MustParseAddr("fe80::1%eth0")) != h.Hash(netip.MustParseAddr("fe80::1")) {
		t.Error("IPv6 zone must not affect the hash")
	}
	if h.Hash(netip.MustParseAddr("2001:db8::1")) == h.Hash(netip.MustParseAddr("2001:db8::2")) {
		t.Error("different IPv6 must differ")
	}
}

func TestRawIPNotInHash(t *testing.T) {
	ip := netip.MustParseAddr("203.0.113.9")
	got := mustNew(t, "s3cret").Hash(ip)
	if bytes.Contains(got[:], ip.AsSlice()) {
		t.Error("hash must not contain the raw IP bytes")
	}
}

func TestEmptySecretRejected(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Error("empty secret must be rejected")
	}
}

func TestSecretIsCopied(t *testing.T) {
	secret := []byte("abc")
	h, _ := New(secret)
	ip := netip.MustParseAddr("1.2.3.4")
	before := h.Hash(ip)
	secret[0] = 'x'
	if h.Hash(ip) != before {
		t.Error("mutating the caller's slice must not change the hasher")
	}
}
