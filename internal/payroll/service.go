package payroll

import (
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("payroll resource not found")
	ErrInvalidState  = errors.New("invalid state for operation")
	ErrNotAuthorized = errors.New("not authorized to perform payroll operation")
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}
