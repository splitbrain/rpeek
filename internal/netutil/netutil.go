// Package netutil holds small address helpers shared by the rpeek server and client so both
// agree on the default port.
package netutil

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// DefaultPort is the TCP port used when an address supplies only a host.
const DefaultPort = "7017"

// NormalizeAddr returns addr as a host:port pair, appending DefaultPort when addr
// supplies only a host. It handles bare hostnames, IPv4 addresses, and IPv6 addresses
// (bracketing them as needed). An address that already includes a port is returned
// unchanged. It returns an error for an empty or unparseable address.
func NormalizeAddr(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", errors.New("empty address")
	}

	if host, port, err := net.SplitHostPort(addr); err == nil {
		if port == "" {
			return net.JoinHostPort(host, DefaultPort), nil
		}
		return addr, nil
	}

	// No port present: treat the whole string as a host and attach the default port.
	// net.JoinHostPort brackets IPv6 hosts correctly.
	candidate := net.JoinHostPort(addr, DefaultPort)
	if _, _, err := net.SplitHostPort(candidate); err != nil {
		return "", fmt.Errorf("invalid address %q: %w", addr, err)
	}
	return candidate, nil
}
