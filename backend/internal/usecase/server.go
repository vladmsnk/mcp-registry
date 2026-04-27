package usecase

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"mcp-registry/internal/audit"
	"mcp-registry/internal/entity"
	"mcp-registry/internal/offboarding"
	"mcp-registry/internal/security"
)

const keycloakClientIDPrefix = "mcp-server-"

type ServerRepo interface {
	List(ctx context.Context) ([]entity.Server, error)
	Create(ctx context.Context, s *entity.Server) error
	GetEndpoint(ctx context.Context, serverID int64) (endpoint, name, keycloakClientID, tlsCertSHA256 string, active bool, err error)
	GetKeycloakInternalID(ctx context.Context, serverID int64) (string, error)
	GetOffboardingMetadata(ctx context.Context, serverID int64) (name, keycloakClientID, keycloakInternalID string, err error)
	UpdateKeycloakInternalID(ctx context.Context, serverID int64, keycloakInternalID string) error
	UpdateTLSPin(ctx context.Context, serverID int64, sha256Hex string) error
	SetActive(ctx context.Context, serverID int64, active bool) error
	Delete(ctx context.Context, serverID int64) (keycloakInternalID string, err error)
}

// ClientProvisioner creates and deletes Keycloak clients for MCP servers.
// RevokeAllTokens is best-effort and may be a no-op if the provisioner doesn't support it.
type ClientProvisioner interface {
	CreateClient(ctx context.Context, clientID string) (keycloakInternalID, secret string, err error)
	RevokeAllTokens(ctx context.Context, keycloakInternalID string) error
	DeleteClient(ctx context.Context, keycloakInternalID string) error
}

type ServerUsecase struct {
	repo        ServerRepo
	provisioner ClientProvisioner          // nil when auth is disabled
	queue       offboarding.Queue          // nil → fall back to logging on KC failure
	auditLog    *audit.Logger              // nil → silent
	urlChecker  *security.URLValidator
	tlsPin      bool // capture+store leaf cert SHA256 on register for HTTPS endpoints
}

type Deps struct {
	Repo              ServerRepo
	Provisioner       ClientProvisioner
	OffboardingQueue  offboarding.Queue
	AuditLog          *audit.Logger
	URLValidator      *security.URLValidator
	TLSPin            bool
}

func NewServerUsecase(d Deps) *ServerUsecase {
	return &ServerUsecase{
		repo:        d.Repo,
		provisioner: d.Provisioner,
		queue:       d.OffboardingQueue,
		auditLog:    d.AuditLog,
		urlChecker:  d.URLValidator,
		tlsPin:      d.TLSPin,
	}
}

func (u *ServerUsecase) List(ctx context.Context) ([]entity.Server, error) {
	servers, err := u.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	if servers == nil {
		servers = []entity.Server{}
	}
	return servers, nil
}

func (u *ServerUsecase) Register(ctx context.Context, s *entity.Server) error {
	if s.Name == "" {
		return errors.New("name is required")
	}
	if s.Endpoint == "" {
		return errors.New("endpoint is required")
	}
	if u.urlChecker != nil {
		if err := u.urlChecker.Validate(ctx, s.Endpoint); err != nil {
			return fmt.Errorf("endpoint validation: %w", err)
		}
	}
	if s.Tags == nil {
		s.Tags = []string{}
	}
	s.Active = true

	// Generate Keycloak client ID.
	s.KeycloakClientID = keycloakClientIDPrefix + s.Name

	// TOFU fingerprint capture for HTTPS endpoints. Failure here aborts registration —
	// without a baseline we cannot verify the server on subsequent calls.
	if u.tlsPin {
		fp, err := security.CaptureFingerprint(ctx, s.Endpoint)
		if err != nil {
			return fmt.Errorf("capture tls fingerprint: %w", err)
		}
		s.TLSCertSHA256 = fp
	}

	// Step 1: Insert to DB.
	if err := u.repo.Create(ctx, s); err != nil {
		return err
	}

	// Step 2: Provision Keycloak client (if auth enabled).
	if u.provisioner != nil {
		keycloakInternalID, _, err := u.provisioner.CreateClient(ctx, s.KeycloakClientID)
		if err != nil {
			// Rollback: delete the DB row.
			if _, delErr := u.repo.Delete(ctx, s.ID); delErr != nil {
				log.Printf("rollback failed for server %d: %v", s.ID, delErr)
			}
			return fmt.Errorf("provision keycloak client: %w", err)
		}

		// Step 3: Store Keycloak internal ID.
		s.KeycloakInternalID = keycloakInternalID
		if err := u.repo.UpdateKeycloakInternalID(ctx, s.ID, keycloakInternalID); err != nil {
			log.Printf("failed to store keycloak internal ID for server %d: %v", s.ID, err)
		}
	}

	return nil
}

// Delete performs cascade offboarding (NHI1):
//  1. Mark inactive — stops new tool calls immediately.
//  2. Snapshot Keycloak metadata (UUID, client_id, name) before the row is gone.
//  3. Push token revocation — on failure, enqueue a retry instead of swallowing.
//  4. Delete Keycloak client — same retry guarantee.
//  5. Delete server row (cascades tools and server_health via FK).
//
// Each Keycloak operation emits an audit event with kc_client_id and outcome
// (allowed/error) so SIEM can detect orphaned-client conditions.
func (u *ServerUsecase) Delete(ctx context.Context, serverID int64) error {
	if err := u.repo.SetActive(ctx, serverID, false); err != nil {
		return fmt.Errorf("deactivate server: %w", err)
	}

	name, kcClientID, internalID, err := u.repo.GetOffboardingMetadata(ctx, serverID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("lookup offboarding metadata: %w", err)
	}

	if u.provisioner != nil && internalID != "" {
		u.runOrEnqueue(ctx, offboarding.OpRevokeTokens, serverID, name, kcClientID, internalID,
			func(c context.Context) error { return u.provisioner.RevokeAllTokens(c, internalID) })
		u.runOrEnqueue(ctx, offboarding.OpDeleteClient, serverID, name, kcClientID, internalID,
			func(c context.Context) error { return u.provisioner.DeleteClient(c, internalID) })
	}

	if _, err := u.repo.Delete(ctx, serverID); err != nil {
		return fmt.Errorf("delete server row: %w", err)
	}

	return nil
}

// runOrEnqueue executes a Keycloak op synchronously; on failure it enqueues a
// retry job so a worker can resume the cleanup. Both outcomes emit an audit
// event with the keycloak client ID so SIEM can correlate.
func (u *ServerUsecase) runOrEnqueue(
	ctx context.Context,
	op offboarding.Op,
	serverID int64,
	serverName, kcClientID, internalID string,
	exec func(context.Context) error,
) {
	opErr := exec(ctx)
	if opErr == nil {
		log.Printf("offboarding: %s succeeded for kc_client_id=%s (server=%s)", op, kcClientID, serverName)
		u.emitAudit(ctx, serverID, op, kcClientID, internalID, serverName, audit.StatusAllowed, "", "inline")
		return
	}

	log.Printf("offboarding: %s failed for kc_client_id=%s: %v", op, kcClientID, opErr)
	queued := false
	if u.queue != nil {
		_, qErr := u.queue.Enqueue(ctx, offboarding.Job{
			Op:                 op,
			KeycloakInternalID: internalID,
			KeycloakClientID:   kcClientID,
			ServerID:           serverID,
			ServerName:         serverName,
			LastError:          opErr.Error(),
			NextAttemptAt:      time.Now().Add(30 * time.Second),
		})
		if qErr != nil {
			log.Printf("offboarding: enqueue retry for %s failed: %v", op, qErr)
		} else {
			queued = true
		}
	}
	phase := "inline_failed"
	if queued {
		phase = "queued_for_retry"
	}
	u.emitAudit(ctx, serverID, op, kcClientID, internalID, serverName, audit.StatusError, opErr.Error(), phase)
}

// RepinTLS re-captures the current leaf cert SHA256 for a server and updates
// the stored pin (P3.12). Audits the old/new fingerprints so SIEM can spot
// off-hours pin churn.
func (u *ServerUsecase) RepinTLS(ctx context.Context, serverID int64) (oldPin, newPin string, err error) {
	endpoint, name, _, oldPin, _, err := u.repo.GetEndpoint(ctx, serverID)
	if err != nil {
		return "", "", fmt.Errorf("lookup server: %w", err)
	}

	newPin, err = security.CaptureFingerprint(ctx, endpoint)
	if err != nil {
		return oldPin, "", fmt.Errorf("capture fingerprint: %w", err)
	}
	if newPin == "" {
		return oldPin, "", errors.New("endpoint is not https; nothing to pin")
	}

	if err := u.repo.UpdateTLSPin(ctx, serverID, newPin); err != nil {
		return oldPin, newPin, fmt.Errorf("update pin: %w", err)
	}

	if u.auditLog != nil {
		u.auditLog.Log(ctx, audit.Event{
			Action:   "server.repin",
			Status:   audit.StatusAllowed,
			ServerID: serverID,
			Metadata: map[string]any{
				"server_name": name,
				"old_pin":     oldPin,
				"new_pin":     newPin,
				"changed":     oldPin != newPin,
			},
		})
	}
	return oldPin, newPin, nil
}

func (u *ServerUsecase) emitAudit(
	ctx context.Context,
	serverID int64,
	op offboarding.Op,
	kcClientID, internalID, serverName, status, errMsg, phase string,
) {
	if u.auditLog == nil {
		return
	}
	u.auditLog.Log(ctx, audit.Event{
		Action:   audit.ActionServerDelete,
		Status:   status,
		ServerID: serverID,
		Error:    errMsg,
		Metadata: map[string]any{
			"phase":                phase,
			"op":                   string(op),
			"keycloak_client_id":   kcClientID,
			"keycloak_internal_id": internalID,
			"server_name":          serverName,
		},
	})
}
