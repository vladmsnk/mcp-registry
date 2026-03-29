package usecase

import (
	"context"
	"errors"

	"mcp-registry/internal/entity"
)

type ServerRepo interface {
	List(ctx context.Context) ([]entity.Server, error)
	Create(ctx context.Context, s *entity.Server) error
	GetEndpoint(ctx context.Context, serverID int64) (endpoint, name string, active bool, err error)
}

type ServerUsecase struct {
	repo ServerRepo
}

func NewServerUsecase(repo ServerRepo) *ServerUsecase {
	return &ServerUsecase{repo: repo}
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
	return u.repo.Create(ctx, s)
}
