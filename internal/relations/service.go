package relations

import (
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("relation case not found")
	ErrInvalidState  = errors.New("invalid case state")
	ErrInvalidAction = errors.New("invalid action parameters")
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool: pool,
	}
}
