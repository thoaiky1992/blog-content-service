package clientip

import (
	"net/netip"
	"testing"
)

func prefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	var out []netip.Prefix
	for _, c := range cidrs {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}

func TestResolve(t *testing.T) {
	trusted := prefixes(t, "10.0.0.0/8", "192.168.0.0/16", "fd00::/8")
	tests := []struct {
		name   string
		cfg    []netip.Prefix
		remote string
		xff    []string
		want   string
	}{
		{"no proxy configured", nil, "203.0.113.5:4000", nil, "203.0.113.5"},
		{"no proxy configured ignores XFF", nil, "203.0.113.5:4000", []string{"1.1.1.1"}, "203.0.113.5"},
		{"untrusted peer ignores forged XFF", trusted, "203.0.113.5:4000", []string{"1.1.1.1"}, "203.0.113.5"},
		{"one proxy", trusted, "10.0.0.1:5000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"one proxy, no XFF", trusted, "10.0.0.1:5000", nil, "10.0.0.1"},
		{"multiple proxies", trusted, "10.0.0.1:5000", []string{"198.51.100.7, 10.0.0.9, 192.168.1.1"}, "198.51.100.7"},
		{"forged left-most is not trusted", trusted, "10.0.0.1:5000", []string{"6.6.6.6, 198.51.100.7, 10.0.0.9"}, "198.51.100.7"},
		{"forger prepends trusted-looking IP", trusted, "10.0.0.1:5000", []string{"10.9.9.9, 198.51.100.7"}, "198.51.100.7"},
		{"multiple XFF header values", trusted, "10.0.0.1:5000", []string{"6.6.6.6", "198.51.100.7, 10.0.0.9"}, "198.51.100.7"},
		{"all hops trusted -> left-most", trusted, "10.0.0.1:5000", []string{"10.1.1.1, 10.2.2.2"}, "10.1.1.1"},
		{"invalid entry -> peer", trusted, "10.0.0.1:5000", []string{"198.51.100.7, not-an-ip"}, "10.0.0.1"},
		{"garbage XFF -> peer", trusted, "10.0.0.1:5000", []string{"%%%"}, "10.0.0.1"},
		{"empty entries skipped", trusted, "10.0.0.1:5000", []string{",, 198.51.100.7 ,"}, "198.51.100.7"},
		{"IPv6 client", trusted, "10.0.0.1:5000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"IPv6 trusted proxy", trusted, "[fd00::1]:5000", []string{"2001:db8::2"}, "2001:db8::2"},
		{"IPv6 peer, no proxy", nil, "[2001:db8::3]:80", nil, "2001:db8::3"},
		{"IPv4-mapped IPv6 peer is unmapped", nil, "[::ffff:203.0.113.5]:80", nil, "203.0.113.5"},
		{"IPv4-mapped IPv6 in XFF is unmapped", trusted, "10.0.0.1:5000", []string{"::ffff:198.51.100.7"}, "198.51.100.7"},
		{"IPv4-mapped trusted hop is still trusted", trusted, "10.0.0.1:5000", []string{"198.51.100.7, ::ffff:10.0.0.9"}, "198.51.100.7"},
		{"zone is dropped", nil, "[fe80::1%eth0]:80", nil, "fe80::1"},
		{"bare IP without port", nil, "203.0.113.5", nil, "203.0.113.5"},
		{"bracketed XFF entry", trusted, "10.0.0.1:5000", []string{"[2001:db8::9]"}, "2001:db8::9"},
	}
	for _, tt := range tests {
		got, ok := New(tt.cfg).Resolve(tt.remote, tt.xff)
		if !ok || got != netip.MustParseAddr(tt.want) {
			t.Errorf("%s: got %v ok=%v, want %s", tt.name, got, ok, tt.want)
		}
	}
}

func TestResolveUnparsablePeer(t *testing.T) {
	if _, ok := New(nil).Resolve("garbage", nil); ok {
		t.Error("unparsable peer must report ok=false")
	}
	if _, ok := New(nil).Resolve("", nil); ok {
		t.Error("empty peer must report ok=false")
	}
}
