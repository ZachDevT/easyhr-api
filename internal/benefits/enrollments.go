package benefits

import (
	"context"
)

type BenefitEnrollment struct {
	ID                string `json:"id"`
	EmployeeID        string `json:"employeeId"`
	EmployeeName      string `json:"employeeName"`
	Department        string `json:"department"`
	PlanID            string `json:"planId"`
	PlanName          string `json:"planName"`
	Category          string `json:"category"`
	CoverageTier      string `json:"coverageTier"`
	Status            string `json:"status"`
	EmployerCostMinor int64  `json:"employerCostMinor"`
	EmployeeCostMinor int64  `json:"employeeCostMinor"`
	Currency          string `json:"currency"`
	EffectiveFrom     string `json:"effectiveFrom"`
}

type CreateEnrollmentInput struct {
	PlanID        string `json:"planId"`
	CoverageTier  string `json:"coverageTier"`
	EffectiveFrom string `json:"effectiveFrom"`
}

func (service *Service) ListEnrollments(ctx context.Context, organizationID, employeeID string) ([]BenefitEnrollment, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			be.id::text, 
			be.employee_id::text, 
			COALESCE(e.first_name || ' ' || e.last_name, 'Team Member') as employee_name,
			COALESCE(d.name, 'General') as department,
			be.plan_id::text, 
			bp.name as plan_name,
			bp.category,
			be.coverage_tier, 
			be.status, 
			bp.employer_cost_minor,
			bp.employee_cost_minor,
			bp.currency,
			be.effective_from::text 
		FROM benefit_enrollments be 
		JOIN benefit_plans bp ON bp.id = be.plan_id
		JOIN employees e ON e.id = be.employee_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE be.organization_id = $1 AND be.employee_id = $2
		ORDER BY be.effective_from DESC
	`, organizationID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []BenefitEnrollment{}
	for rows.Next() {
		var item BenefitEnrollment
		if err := rows.Scan(
			&item.ID, &item.EmployeeID, &item.EmployeeName, &item.Department,
			&item.PlanID, &item.PlanName, &item.Category, &item.CoverageTier,
			&item.Status, &item.EmployerCostMinor, &item.EmployeeCostMinor,
			&item.Currency, &item.EffectiveFrom,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) ListAllEnrollments(ctx context.Context, organizationID string) ([]BenefitEnrollment, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			be.id::text, 
			be.employee_id::text, 
			COALESCE(e.first_name || ' ' || e.last_name, 'Team Member') as employee_name,
			COALESCE(d.name, 'General') as department,
			be.plan_id::text, 
			bp.name as plan_name,
			bp.category,
			be.coverage_tier, 
			be.status, 
			bp.employer_cost_minor,
			bp.employee_cost_minor,
			bp.currency,
			be.effective_from::text 
		FROM benefit_enrollments be 
		JOIN benefit_plans bp ON bp.id = be.plan_id
		JOIN employees e ON e.id = be.employee_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE be.organization_id = $1
		ORDER BY be.effective_from DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []BenefitEnrollment{}
	for rows.Next() {
		var item BenefitEnrollment
		if err := rows.Scan(
			&item.ID, &item.EmployeeID, &item.EmployeeName, &item.Department,
			&item.PlanID, &item.PlanName, &item.Category, &item.CoverageTier,
			&item.Status, &item.EmployerCostMinor, &item.EmployeeCostMinor,
			&item.Currency, &item.EffectiveFrom,
		); err != nil {
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
	var firstName, lastName string
	var deptName *string
	err = tx.QueryRow(ctx, `
		SELECT true, e.first_name, e.last_name, d.name 
		FROM employees e 
		LEFT JOIN departments d ON d.id = e.department_id 
		WHERE e.id = $1 AND e.organization_id = $2
	`, employeeID, organizationID).Scan(&exists, &firstName, &lastName, &deptName)
	if err != nil || !exists {
		return BenefitEnrollment{}, ErrNotFound
	}

	// Verify plan
	var planName, category, currency string
	var employerCost, employeeCost int64
	err = tx.QueryRow(ctx, `
		SELECT true, name, category, currency, employer_cost_minor, employee_cost_minor 
		FROM benefit_plans 
		WHERE id = $1 AND organization_id = $2
	`, input.PlanID, organizationID).Scan(&exists, &planName, &category, &currency, &employerCost, &employeeCost)
	if err != nil || !exists {
		return BenefitEnrollment{}, ErrNotFound
	}

	if input.CoverageTier == "" {
		input.CoverageTier = "individual"
	}

	var item BenefitEnrollment
	item.EmployeeName = firstName + " " + lastName
	if deptName != nil {
		item.Department = *deptName
	} else {
		item.Department = "General"
	}
	item.PlanName = planName
	item.Category = category
	item.Currency = currency
	item.EmployerCostMinor = employerCost
	item.EmployeeCostMinor = employeeCost

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
