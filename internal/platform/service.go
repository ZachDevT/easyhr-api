package platform

import (
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
)

type Service struct {
	pool      *pgxpool.Pool
	jwtSecret string
}

func NewService(pool *pgxpool.Pool, jwtSecret string) *Service {
	return &Service{
		pool:      pool,
		jwtSecret: jwtSecret,
	}
}
