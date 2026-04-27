package auth

import (
	"net/http"

	"mcp-registry/internal/audit"
)

// HasAnyRole reports whether claims grant at least one of the required roles.
// An empty required set means no role restriction.
func HasAnyRole(claims *Claims, required []string) bool {
	if len(required) == 0 {
		return true
	}
	if claims == nil {
		return false
	}
	have := make(map[string]struct{}, len(claims.RealmRoles))
	for _, r := range claims.RealmRoles {
		have[r] = struct{}{}
	}
	for _, r := range required {
		if _, ok := have[r]; ok {
			return true
		}
	}
	return false
}

// RequireRole returns a middleware that allows the request only if the caller has at least one of the roles.
// Denials are recorded via auditLog (if non-nil) and respond 403.
func RequireRole(auditLog *audit.Logger, roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if HasAnyRole(claims, roles) {
				next.ServeHTTP(w, r)
				return
			}

			meta := map[string]any{
				"path":           r.URL.Path,
				"method":         r.Method,
				"required_roles": roles,
				"denial_reason":  "no_role_overlap",
			}
			// Best-effort extraction of resource identifiers from the route
			// (so a deny event names *what* was denied, not just the URL).
			if v := r.PathValue("id"); v != "" {
				meta["target_server_id"] = v
			}
			if v := r.PathValue("name"); v != "" {
				meta["tool_name"] = v
			}
			ev := audit.Event{
				Action:    audit.ActionAuthDeny,
				Status:    audit.StatusDenied,
				IP:        audit.ClientIP(r),
				UserAgent: r.Header.Get("User-Agent"),
				RequestID: r.Header.Get("X-Request-ID"),
				Error:     "missing required role",
				Metadata:  meta,
			}
			if claims != nil {
				ev.ActorSub = claims.Subject
				ev.ActorUsername = claims.PreferredUsername
				ev.ActorRoles = claims.RealmRoles
				meta["caller_roles"] = claims.RealmRoles
			}
			auditLog.Log(r.Context(), ev)

			http.Error(w, `{"error":"forbidden: missing required role"}`, http.StatusForbidden)
		})
	}
}
