package platform

import (
	"context"
	"golang.org/x/crypto/bcrypt"
)

type Operator struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Status     string `json:"status"`
	MfaEnabled bool   `json:"mfaEnabled"`
}

type CreateOperatorInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Service) ListOperators(ctx context.Context) ([]Operator, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, email, status, mfa_enabled FROM platform_operators ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Operator
	for rows.Next() {
		var o Operator
		if err := rows.Scan(&o.ID, &o.Email, &o.Status, &o.MfaEnabled); err != nil {
			return nil, err
		}
		items = append(items, o)
	}
	return items, nil
}

func (s *Service) CreateOperator(ctx context.Context, input CreateOperatorInput) (Operator, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return Operator{}, err
	}

	var o Operator
	err = s.pool.QueryRow(ctx, `
		INSERT INTO platform_operators (email, password_hash)
		VALUES ($1, $2)
		RETURNING id::text, email, status, mfa_enabled
	`, input.Email, string(hash)).Scan(&o.ID, &o.Email, &o.Status, &o.MfaEnabled)

	return o, err
}
