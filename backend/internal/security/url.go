package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var (
	ErrInvalidScheme  = errors.New("invalid url scheme")
	ErrHostNotAllowed = errors.New("host not in allowlist")
	ErrPrivateIP      = errors.New("endpoint resolves to private or loopback IP")
	ErrMissingHost    = errors.New("url has no host")
)

// URLValidator vets endpoints submitted by callers (e.g. on /api/servers register).
// All fields default to permissive: zero value validates only that the URL parses
// and uses http(s).
type URLValidator struct {
	RequireHTTPS    bool
	AllowedHosts    []string
	BlockPrivateIPs bool

	// Resolver is used for DNS lookups when BlockPrivateIPs is true.
	// If nil, net.DefaultResolver is used.
	Resolver *net.Resolver
}

// Validate parses the URL and applies all configured checks. Returns a wrapped
// sentinel error on failure so callers can inspect the reason via errors.Is.
func (v *URLValidator) Validate(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}

	switch u.Scheme {
	case "https":
		// always ok
	case "http":
		if v.RequireHTTPS {
			return fmt.Errorf("%w: got %q, https required", ErrInvalidScheme, u.Scheme)
		}
	default:
		return fmt.Errorf("%w: %q (only http/https allowed)", ErrInvalidScheme, u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return ErrMissingHost
	}

	if len(v.AllowedHosts) > 0 && !hostAllowed(host, v.AllowedHosts) {
		return fmt.Errorf("%w: %q", ErrHostNotAllowed, host)
	}

	if v.BlockPrivateIPs {
		resolver := v.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		ips, err := resolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return fmt.Errorf("dns lookup %q: %w", host, err)
		}
		for _, ip := range ips {
			if isPrivateOrLocal(ip) {
				return fmt.Errorf("%w: %s → %s", ErrPrivateIP, host, ip.String())
			}
		}
	}

	return nil
}

func hostAllowed(host string, list []string) bool {
	h := strings.ToLower(host)
	for _, allowed := range list {
		a := strings.ToLower(strings.TrimSpace(allowed))
		if a == "" {
			continue
		}
		if a == h {
			return true
		}
		// Wildcard suffix support: "*.example.com" matches "foo.example.com" and "example.com".
		if strings.HasPrefix(a, "*.") {
			suffix := a[1:]
			if strings.HasSuffix(h, suffix) {
				return true
			}
		}
	}
	return false
}

// isPrivateOrLocal reports whether the IP is one we should never call from the hub:
// loopback, link-local, RFC1918 private, carrier-grade NAT (100.64.0.0/10), or unspecified.
func isPrivateOrLocal(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// Carrier-grade NAT: 100.64.0.0/10 — not covered by IsPrivate.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
	}
	return false
}
