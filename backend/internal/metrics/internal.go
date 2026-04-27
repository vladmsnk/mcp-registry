// Package metrics exposes a single non-public dashboard endpoint surfacing the
// inputs the security team needs to spot drift (P3.15):
//
//   - Keycloak admin secret age (proxy: when the secret was loaded into memory)
//   - Admin-token refresh count
//   - TLS pin age per server (operator should re-pin if too old)
//
// Output is JSON, not Prometheus, because the call site is "operator opens a
// dashboard" not "scrape every 15s". The endpoint is gated by a shared token
// (METRICS_TOKEN env) so no additional auth wiring is needed.
package metrics

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// AdminClientStatus is the projection of keycloak.AdminClient the metrics
// endpoint reads. Decoupled to avoid an import cycle.
type AdminClientStatus interface {
	SecretCapturedAt() time.Time
	RefreshCount() int64
}

// PinSummary describes a single server's pin age.
type PinSummary struct {
	ServerID    int64      `json:"server_id"`
	ServerName  string     `json:"server_name"`
	HasPin      bool       `json:"has_pin"`
	CapturedAt  *time.Time `json:"captured_at,omitempty"`
	AgeDays     *float64   `json:"age_days,omitempty"`
	StaleWarning bool      `json:"stale_warning"`
}

// Snapshot is the JSON payload returned by the endpoint.
type Snapshot struct {
	GeneratedAt          time.Time    `json:"generated_at"`
	AdminSecretAgeDays   float64      `json:"admin_secret_age_days"`
	AdminTokenRefreshes  int64        `json:"admin_token_refreshes"`
	StalePinThresholdDays int         `json:"stale_pin_threshold_days"`
	Pins                 []PinSummary `json:"pins"`
}

// Handler builds the http.Handler for /internal/metrics.
type Handler struct {
	db                    *sql.DB
	admin                 AdminClientStatus
	stalePinThresholdDays int
	authToken             string
}

func NewHandler(db *sql.DB, admin AdminClientStatus, stalePinThresholdDays int, authToken string) *Handler {
	if stalePinThresholdDays <= 0 {
		stalePinThresholdDays = 90
	}
	return &Handler{db: db, admin: admin, stalePinThresholdDays: stalePinThresholdDays, authToken: authToken}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	snap, err := h.snapshot(r.Context())
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(snap)
}

func (h *Handler) authorize(r *http.Request) bool {
	if h.authToken == "" {
		// No token configured → endpoint is bound to localhost via the mux.
		// The operator who set this up either accepts that or sets METRICS_TOKEN.
		return true
	}
	got := r.Header.Get("X-Metrics-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.authToken)) == 1
}

func (h *Handler) snapshot(ctx context.Context) (Snapshot, error) {
	now := time.Now()
	snap := Snapshot{
		GeneratedAt:           now,
		StalePinThresholdDays: h.stalePinThresholdDays,
	}
	if h.admin != nil {
		captured := h.admin.SecretCapturedAt()
		if !captured.IsZero() {
			snap.AdminSecretAgeDays = now.Sub(captured).Hours() / 24
		}
		snap.AdminTokenRefreshes = h.admin.RefreshCount()
	}

	rows, err := h.db.QueryContext(ctx,
		`SELECT id, name, tls_cert_sha256, tls_cert_captured_at FROM servers ORDER BY id`)
	if err != nil {
		return snap, err
	}
	defer rows.Close()

	threshold := time.Duration(h.stalePinThresholdDays) * 24 * time.Hour
	for rows.Next() {
		var (
			id         int64
			name, hash string
			capturedAt sql.NullTime
		)
		if err := rows.Scan(&id, &name, &hash, &capturedAt); err != nil {
			return snap, err
		}
		ps := PinSummary{ServerID: id, ServerName: name, HasPin: hash != ""}
		if capturedAt.Valid {
			ps.CapturedAt = &capturedAt.Time
			age := now.Sub(capturedAt.Time)
			ageDays := age.Hours() / 24
			ps.AgeDays = &ageDays
			ps.StaleWarning = age > threshold
		}
		snap.Pins = append(snap.Pins, ps)
	}
	return snap, rows.Err()
}
