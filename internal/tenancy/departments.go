package tenancy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDepartmentNotFound = errors.New("department reference not found")
var ErrDepartmentConflict = errors.New("department name or code already exists")

type DepartmentService struct{ pool *pgxpool.Pool }

func NewDepartmentService(pool *pgxpool.Pool) *DepartmentService {
	return &DepartmentService{pool: pool}
}

type Department struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Code               *string `json:"code"`
	DivisionID         *string `json:"divisionId"`
	ParentDepartmentID *string `json:"parentDepartmentId"`
	Status             string  `json:"status"`
}

type CreateDepartmentInput struct {
	Name               string `json:"name"`
	Code               string `json:"code"`
	DivisionID         string `json:"divisionId"`
	ParentDepartmentID string `json:"parentDepartmentId"`
}

func (input CreateDepartmentInput) Validate() error {
	if name := strings.TrimSpace(input.Name); name == "" || len(name) > 160 {
		return fmt.Errorf("name must contain between 1 and 160 characters")
	}
	if len(strings.TrimSpace(input.Code)) > 80 {
		return fmt.Errorf("code must not exceed 80 characters")
	}
	return nil
}

func (service *DepartmentService) List(ctx context.Context, organizationID string) ([]Department, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text, name, code, division_id::text, parent_department_id::text, status FROM departments WHERE organization_id = $1 ORDER BY name ASC, id ASC`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Department, 0)
	for rows.Next() {
		var item Department
		if err = rows.Scan(&item.ID, &item.Name, &item.Code, &item.DivisionID, &item.ParentDepartmentID, &item.Status); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (service *DepartmentService) Create(ctx context.Context, organizationID, actorUserID string, input CreateDepartmentInput) (Department, error) {
	if err := input.Validate(); err != nil {
		return Department{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Department{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, reference := range []struct{ table, id string }{{"divisions", input.DivisionID}, {"departments", input.ParentDepartmentID}} {
		if strings.TrimSpace(reference.id) == "" {
			continue
		}
		var exists bool
		query := fmt.Sprintf("SELECT EXISTS(SELECT 1 FROM %s WHERE id = $1 AND organization_id = $2)", reference.table)
		if err = tx.QueryRow(ctx, query, strings.TrimSpace(reference.id), organizationID).Scan(&exists); err != nil {
			return Department{}, err
		}
		if !exists {
			return Department{}, ErrDepartmentNotFound
		}
	}
	result := Department{Name: strings.TrimSpace(input.Name), Status: "active"}
	err = tx.QueryRow(ctx, `INSERT INTO departments (organization_id, division_id, parent_department_id, name, code) VALUES ($1, NULLIF($2, '')::uuid, NULLIF($3, '')::uuid, $4, NULLIF($5, '')) RETURNING id::text, code, division_id::text, parent_department_id::text`, organizationID, strings.TrimSpace(input.DivisionID), strings.TrimSpace(input.ParentDepartmentID), result.Name, strings.TrimSpace(input.Code)).Scan(&result.ID, &result.Code, &result.DivisionID, &result.ParentDepartmentID)
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" {
			return Department{}, ErrDepartmentConflict
		}
		return Department{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id) VALUES ($1, $2, 'department.created', 'department', $3, 'unknown')`, organizationID, actorUserID, result.ID); err != nil {
		return Department{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Department{}, err
	}
	return result, nil
}
