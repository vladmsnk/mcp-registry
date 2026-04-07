package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"

	"mcp-registry/internal/entity"
)

const keycloakClientIDPrefix = "mcp-server-"

type ServerRepo interface {
	List(ctx context.Context) ([]entity.Server, error)
	Create(ctx context.Context, s *entity.Server) error
	GetEndpoint(ctx context.Context, serverID int64) (endpoint, name, keycloakClientID string, active bool, err error)
	UpdateKeycloakInternalID(ctx context.Context, serverID int64, keycloakInternalID string) error
	Delete(ctx context.Context, serverID int64) (keycloakInternalID string, err error)
}

// ClientProvisioner creates and deletes Keycloak clients for MCP servers.
type ClientProvisioner interface {
	CreateClient(ctx context.Context, clientID string) (keycloakInternalID, secret string, err error)
	DeleteClient(ctx context.Context, keycloakInternalID string) error
}

type ServerUsecase struct {
	repo        ServerRepo
	provisioner ClientProvisioner // nil when auth is disabled
}

func NewServerUsecase(repo ServerRepo, provisioner ClientProvisioner) *ServerUsecase {
	return &ServerUsecase{repo: repo, provisioner: provisioner}
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
	if s.Tags == nil {
		s.Tags = []string{}
	}
	s.Active = true

	// Generate Keycloak client ID.
	s.KeycloakClientID = keycloakClientIDPrefix + s.Name

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

func (u *ServerUsecase) Delete(ctx context.Context, serverID int64) error {
	keycloakInternalID, err := u.repo.Delete(ctx, serverID)
	if err != nil {
		return fmt.Errorf("delete server: %w", err)
	}

	// Clean up Keycloak client if it was provisioned.
	if u.provisioner != nil && keycloakInternalID != "" {
		if err := u.provisioner.DeleteClient(ctx, keycloakInternalID); err != nil {
			log.Printf("failed to delete keycloak client %s: %v", keycloakInternalID, err)
		}
	}

	return nil
}
