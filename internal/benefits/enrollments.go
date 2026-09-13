package benefits

import (
	"context"
)

type BenefitEnrollment struct {
	ID            string `json:"id"`
	EmployeeID    string `json:"employeeId"`
	PlanID        string `json:"planId"`
	CoverageTier  string `json:"coverageTier"`
	Status        string `json:"status"`
	EffectiveFrom string `json:"effectiveFrom"`
}

type CreateEnrollmentInput struct {
	PlanID        string `json:"planId"`
	CoverageTier  string `json:"coverageTier"`
	EffectiveFrom string `json:"effectiveFrom"`
}

func (service *Service) ListEnrollments(ctx context.Context, organizationID, employeeID string) ([]BenefitEnrollment, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, employee_id::text, plan_id::text, coverage_tier, status, effective_from::text 
		FROM benefit_enrollments 
		WHERE organization_id = $1 AND employee_id = $2
		ORDER BY effective_from DESC
	`, organizationID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []BenefitEnrollment{}
	for rows.Next() {
		var item BenefitEnrollment
		if err := rows.Scan(&item.ID, &item.EmployeeID, &item.PlanID, &item.CoverageTier, &item.Status, &item.EffectiveFrom); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) CreateEnrollment(ctx context.Context, organizationID, actorUserID, employeeID string, input CreateEnrollmentInput) (BenefitEnrollment, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return BenefitEnrollment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify employee
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, employeeID, organizationID).Scan(&exists)
	if err != nil || !exists {
		return BenefitEnrollment{}, ErrNotFound
	}

	// Verify plan
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM benefit_plans WHERE id = $1 AND organization_id = $2)`, input.PlanID, organizationID).Scan(&exists)
	if err != nil || !exists {
		return BenefitEnrollment{}, ErrNotFound
	}

	var item BenefitEnrollment
	err = tx.QueryRow(ctx, `
		INSERT INTO benefit_enrollments (organization_id, employee_id, plan_id, coverage_tier, status, effective_from, created_by_user_id)
		VALUES ($1, $2, $3, $4, 'active', $5, $6)
		RETURNING id::text, employee_id::text, plan_id::text, coverage_tier, status, effective_from::text
	`, organizationID, employeeID, input.PlanID, input.CoverageTier, input.EffectiveFrom, actorUserID).Scan(
		&item.ID, &item.EmployeeID, &item.PlanID, &item.CoverageTier, &item.Status, &item.EffectiveFrom,
	)
	if err != nil {
		return BenefitEnrollment{}, err
	}

	return item, tx.Commit(ctx)
}
