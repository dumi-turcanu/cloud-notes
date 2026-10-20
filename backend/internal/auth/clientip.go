package auth

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the client address. With hops == 0 only the TCP peer is
// trusted. With hops == N > 0 the proxies we control append exactly N entries
// to X-Forwarded-For, so the real client is entry len-N (anything to its left
// is attacker-supplied). With fewer than N entries it falls back to the peer.
func ClientIP(r *http.Request, hops int) string {
	peer := r.RemoteAddr
	if host, _, err := net.SplitHostPort(peer); err == nil {
		peer = host
	}
	if hops <= 0 {
		return peer
	}
	var entries []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for _, e := range strings.Split(v, ",") {
			entries = append(entries, strings.TrimSpace(e))
		}
	}
	if len(entries) < hops {
		return peer
	}
	ip := entries[len(entries)-hops]
	if net.ParseIP(ip) == nil {
		return peer
	}
	return ip
}
