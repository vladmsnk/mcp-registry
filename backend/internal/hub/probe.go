package hub

import (
	"context"

	"mcp-registry/internal/security"
)

// ProbeTokenSource produces a bearer token for probe requests. nil → probe
// runs unauthenticated (legacy behaviour). When set, the returned token is
// attached as Authorization: Bearer to every probe `initialize`.
type ProbeTokenSource func(ctx context.Context) (string, error)

// ProbeOptions controls how a single probe is executed.
type ProbeOptions struct {
	// PinSHA256 is the per-server pinned leaf-cert fingerprint. Empty disables
	// pinning. P1.8 flips this to "enforced when set" so a live MITM is caught
	// the same way it is on user-driven calls.
	PinSHA256 string
	// TokenSource produces the bearer token for the probe. nil = unauthenticated.
	TokenSource ProbeTokenSource
}

// ProbeMCPServer performs an MCP `initialize` against the endpoint with the
// hub's TLS/SSRF guard. Pass ProbeOptions to enable pinning and authenticated
// probing (P1.8). The legacy unauthenticated, unpinned form is preserved by
// passing the zero value of ProbeOptions.
func ProbeMCPServer(ctx context.Context, endpoint string, httpOpts security.ClientOptions, opts ProbeOptions) error {
	var bearer string
	if opts.TokenSource != nil {
		t, err := opts.TokenSource(ctx)
		if err != nil {
			// Fall through unauthenticated rather than failing the probe — a token
			// outage is its own observability signal but should not falsely flip
			// every server to "unhealthy".
			bearer = ""
		} else {
			bearer = t
		}
	}
	client := buildClient(httpOpts, opts.PinSHA256)
	sess := newMCPSession(endpoint, bearer, client)
	return sess.initialize(ctx)
}
