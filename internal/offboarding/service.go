package offboarding

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

type Plan struct {
	ID                 string  `json:"id"`
	EmployeeID         string  `json:"employeeId"`
	OwnerID            *string `json:"ownerId"`
	LastWorkingDay     string  `json:"lastWorkingDay"`
	Reason             string  `json:"reason"`
	RiskStatus         string  `json:"riskStatus"`
	ProgressPercentage int     `json:"progressPercentage"`
}

type CreatePlanInput struct {
	EmployeeID     string  `json:"employeeId"`
	OwnerID        *string `json:"ownerId"`
	LastWorkingDay string  `json:"lastWorkingDay"`
	Reason         string  `json:"reason"`
}

func (s *Service) ListPlans(ctx context.Context, orgID string) ([]Plan, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, employee_id::text, owner_id::text, last_working_day::text, reason, risk_status, progress_percentage FROM offboarding_plans WHERE organization_id = $1 ORDER BY created_at DESC", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Plan
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.EmployeeID, &p.OwnerID, &p.LastWorkingDay, &p.Reason, &p.RiskStatus, &p.ProgressPercentage); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}

func (s *Service) CreatePlan(ctx context.Context, orgID, userID string, input CreatePlanInput) (Plan, error) {
	var p Plan
	err := s.pool.QueryRow(ctx, `
		INSERT INTO offboarding_plans (organization_id, employee_id, owner_id, last_working_day, reason, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, employee_id::text, owner_id::text, last_working_day::text, reason, risk_status, progress_percentage
	`, orgID, input.EmployeeID, input.OwnerID, input.LastWorkingDay, input.Reason, userID).Scan(
		&p.ID, &p.EmployeeID, &p.OwnerID, &p.LastWorkingDay, &p.Reason, &p.RiskStatus, &p.ProgressPercentage,
	)
	return p, err
}
