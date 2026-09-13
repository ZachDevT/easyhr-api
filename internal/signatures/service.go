package signatures

import (
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("signature request not found")
	ErrProviderError = errors.New("electronic signature provider error")
)

type Service struct {
	pool     *pgxpool.Pool
	provider Provider
}

func NewService(pool *pgxpool.Pool, provider Provider) *Service {
	return &Service{
		pool:     pool,
		provider: provider,
	}
}
