package security

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrFingerprintMismatch = errors.New("server tls fingerprint mismatch")
	ErrEgressNotAllowed    = errors.New("egress destination not in allowlist")
)

// ClientOptions configures the safe HTTP client used to talk to downstream MCP servers.
type ClientOptions struct {
	Timeout         time.Duration
	BlockPrivateIPs bool

	// PinSHA256 is the lowercase hex sha256 of the leaf certificate. Empty disables pinning.
	PinSHA256 string

	// EgressAllowlist (P3.14) restricts the hosts/IPs the client can dial. Each
	// entry is a hostname (exact match), an IP, or a CIDR block. Empty disables
	// the check (private-IP block still applies if BlockPrivateIPs=true).
	EgressAllowlist []string
}

// NewClient returns an *http.Client with TLS 1.2+, optional SSRF guard via DialContext,
// and optional leaf-cert fingerprint pinning. It is safe for reuse across requests.
func NewClient(opts ClientOptions) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}

	allowlist := parseEgressAllowlist(opts.EgressAllowlist)

	var dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	switch {
	case opts.BlockPrivateIPs || allowlist != nil:
		dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return safeDial(ctx, dialer, network, addr, opts.BlockPrivateIPs, allowlist)
		}
	default:
		dialContext = dialer.DialContext
	}

	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if opts.PinSHA256 != "" {
		expected := strings.ToLower(opts.PinSHA256)
		tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no peer certificates")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			got := hex.EncodeToString(sum[:])
			if got != expected {
				return fmt.Errorf("%w: expected %s, got %s", ErrFingerprintMismatch, expected, got)
			}
			return nil
		}
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialContext,
			TLSClientConfig:       tlsCfg,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 20 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     true,
		},
	}
}

// egressAllowlist holds the parsed allowlist for fast checks. nil means "no
// allowlist configured" (the dial proceeds subject only to BlockPrivateIPs).
type egressAllowlist struct {
	hosts map[string]struct{}
	ips   []net.IP
	cidrs []*net.IPNet
}

func parseEgressAllowlist(entries []string) *egressAllowlist {
	if len(entries) == 0 {
		return nil
	}
	a := &egressAllowlist{hosts: map[string]struct{}{}}
	for _, raw := range entries {
		e := strings.ToLower(strings.TrimSpace(raw))
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			a.cidrs = append(a.cidrs, n)
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			a.ips = append(a.ips, ip)
			continue
		}
		a.hosts[e] = struct{}{}
	}
	if len(a.hosts) == 0 && len(a.ips) == 0 && len(a.cidrs) == 0 {
		return nil
	}
	return a
}

func (a *egressAllowlist) allows(host string, ip net.IP) bool {
	if a == nil {
		return true
	}
	if _, ok := a.hosts[strings.ToLower(host)]; ok {
		return true
	}
	for _, allowed := range a.ips {
		if allowed.Equal(ip) {
			return true
		}
	}
	for _, n := range a.cidrs {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func safeDial(ctx context.Context, dialer *net.Dialer, network, addr string, blockPrivate bool, allowlist *egressAllowlist) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("dns lookup %q: %w", host, err)
	}
	var allowed []net.IP
	for _, ip := range ips {
		if blockPrivate && isPrivateOrLocal(ip) {
			continue
		}
		if !allowlist.allows(host, ip) {
			continue
		}
		allowed = append(allowed, ip)
	}
	if len(allowed) == 0 {
		// Distinguish the two failure modes so audit/log makes sense.
		if allowlist != nil {
			return nil, fmt.Errorf("%w: %s", ErrEgressNotAllowed, host)
		}
		return nil, fmt.Errorf("%w: %s", ErrPrivateIP, host)
	}
	var lastErr error
	for _, ip := range allowed {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// CaptureFingerprint connects to the URL and returns the leaf cert SHA256 hex (lowercase).
// For HTTP URLs it returns "", nil — there is no cert to pin.
func CaptureFingerprint(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" {
		return "", nil
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":443"
	}

	d := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 5 * time.Second},
		Config:    &tls.Config{MinVersion: tls.VersionTLS12},
	}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return "", fmt.Errorf("tls dial %q: %w", host, err)
	}
	defer conn.Close()

	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return "", errors.New("not a tls connection")
	}
	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("no peer certificates")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}
