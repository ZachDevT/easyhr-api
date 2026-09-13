package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDivisionConflict = errors.New("division name or code already exists")

type DivisionService struct{ pool *pgxpool.Pool }

func NewDivisionService(pool *pgxpool.Pool) *DivisionService { return &DivisionService{pool: pool} }

type Division struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Code   *string `json:"code"`
	Status string  `json:"status"`
}

type CreateDivisionInput struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

func (input CreateDivisionInput) Validate() error {
	if name := strings.TrimSpace(input.Name); name == "" || len(name) > 160 {
		return fmt.Errorf("name must contain between 1 and 160 characters")
	}
	if len(strings.TrimSpace(input.Code)) > 80 {
		return fmt.Errorf("code must not exceed 80 characters")
	}
	return nil
}

func (service *DivisionService) List(ctx context.Context, organizationID string) ([]Division, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text, name, code, status FROM divisions WHERE organization_id = $1 ORDER BY name ASC, id ASC`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Division, 0)
	for rows.Next() {
		var item Division
		if err = rows.Scan(&item.ID, &item.Name, &item.Code, &item.Status); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (service *DivisionService) Create(ctx context.Context, organizationID, actorUserID string, input CreateDivisionInput) (Division, error) {
	if err := input.Validate(); err != nil {
		return Division{}, err
	}
	result := Division{Name: strings.TrimSpace(input.Name), Status: "active"}
	err := service.pool.QueryRow(ctx, `INSERT INTO divisions (organization_id, name, code) VALUES ($1, $2, NULLIF($3, '')) RETURNING id::text, code`, organizationID, result.Name, strings.TrimSpace(input.Code)).Scan(&result.ID, &result.Code)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return Division{}, ErrDivisionConflict
		}
		return Division{}, err
	}
	if _, err = service.pool.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id) VALUES ($1, $2, 'division.created', 'division', $3, 'unknown')`, organizationID, actorUserID, result.ID); err != nil {
		return Division{}, err
	}
	return result, nil
}
