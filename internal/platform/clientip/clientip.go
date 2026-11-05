// Package clientip resolves the real client IP behind trusted reverse proxies.
package clientip

import (
	"net"
	"net/netip"
	"strings"
)

// Resolver implements the trusted-proxy rule from the design (section 12).
type Resolver struct {
	trusted []netip.Prefix
}

// New builds a Resolver. With no trusted prefixes, forwarded headers are never
// trusted and the direct peer address is always used.
func New(trusted []netip.Prefix) *Resolver { return &Resolver{trusted: trusted} }

func (r *Resolver) isTrusted(a netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Resolve returns the client IP.
//
//   - If the direct peer is not trusted, forwarded headers are ignored (they
//     could be forged) and the peer address is returned.
//   - If it is trusted, X-Forwarded-For is walked from right to left. Trusted
//     hops are skipped and the first untrusted hop is the client. The left-most
//     entry is never trusted blindly.
//   - If an entry is not a valid IP, the header is considered corrupt and the
//     peer address is returned (safe: never an attacker-chosen value).
//   - If every hop is trusted, the left-most hop is returned.
//
// xff holds every X-Forwarded-For header value of the request.
// ok is false only when the peer address itself cannot be parsed.
func (r *Resolver) Resolve(remoteAddr string, xff []string) (ip netip.Addr, ok bool) {
	peer, ok := parsePeer(remoteAddr)
	if !ok {
		return netip.Addr{}, false
	}
	if !r.isTrusted(peer) {
		return peer, true
	}

	var hops []string
	for _, v := range xff {
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				hops = append(hops, p)
			}
		}
	}
	if len(hops) == 0 {
		return peer, true
	}

	var leftMost netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := parseIP(hops[i])
		if err != nil {
			return peer, true
		}
		if !r.isTrusted(a) {
			return a, true
		}
		leftMost = a
	}
	return leftMost, true
}

func parsePeer(remoteAddr string) (netip.Addr, bool) {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	a, err := parseIP(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a, true
}

// parseIP parses an IP, normalizing IPv4-mapped IPv6 to IPv4 and dropping any
// IPv6 zone so equal addresses always compare equal.
func parseIP(s string) (netip.Addr, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimSuffix(s, "]"), "[")
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, err
	}
	return a.WithZone("").Unmap(), nil
}
