package benefits

import (
	"context"
)

type BenefitPlan struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Category          string  `json:"category"`
	Description       *string `json:"description"`
	EmployerCostMinor int64   `json:"employerCostMinor"`
	EmployeeCostMinor int64   `json:"employeeCostMinor"`
	Currency          string  `json:"currency"`
	EnrolledCount     int     `json:"enrolledCount"`
	EligibleCount     int     `json:"eligibleCount"`
}

type CreatePlanInput struct {
	Name              string  `json:"name"`
	Category          string  `json:"category"`
	Description       *string `json:"description"`
	EmployerCostMinor int64   `json:"employerCostMinor"`
	EmployeeCostMinor int64   `json:"employeeCostMinor"`
	Currency          string  `json:"currency"`
}

func (service *Service) ListPlans(ctx context.Context, organizationID string) ([]BenefitPlan, error) {
	// Check if plans exist; if not, seed default starter plans for the organization
	var count int
	_ = service.pool.QueryRow(ctx, `SELECT count(*) FROM benefit_plans WHERE organization_id = $1`, organizationID).Scan(&count)
	if count == 0 {
		_ = service.seedDefaultPlans(ctx, organizationID)
	}

	var eligibleCount int
	_ = service.pool.QueryRow(ctx, `SELECT count(*) FROM employees WHERE organization_id = $1`, organizationID).Scan(&eligibleCount)
	if eligibleCount == 0 {
		eligibleCount = 1
	}

	rows, err := service.pool.Query(ctx, `
		SELECT 
			p.id::text, 
			p.name, 
			p.category, 
			p.description, 
			p.employer_cost_minor, 
			p.employee_cost_minor, 
			p.currency,
			COALESCE(COUNT(DISTINCT e.id), 0)::int as enrolled_count
		FROM benefit_plans p
		LEFT JOIN benefit_enrollments e ON e.plan_id = p.id AND e.status = 'active'
		WHERE p.organization_id = $1 
		GROUP BY p.id
		ORDER BY p.created_at ASC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []BenefitPlan{}
	for rows.Next() {
		var item BenefitPlan
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Category, &item.Description,
			&item.EmployerCostMinor, &item.EmployeeCostMinor, &item.Currency,
			&item.EnrolledCount,
		); err != nil {
			return nil, err
		}
		item.EligibleCount = eligibleCount
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) seedDefaultPlans(ctx context.Context, organizationID string) error {
	defaults := []struct {
		name     string
		category string
		desc     string
		employer int64
		employee int64
	}{
		{"Health & Medical Insurance", "health", "Comprehensive medical, dental, and hospitalization care.", 45000, 5000},
		{"401(k) / Retirement Pension", "retirement", "Company matches up to 4% of employee retirement savings.", 20000, 20000},
		{"Meal & Wellness Allowance", "allowance", "Monthly stipend for nutritious meals, fitness, and wellness.", 15000, 0},
		{"Group Life & Accident Insurance", "health", "Company-paid policy covering 2x annual base compensation.", 5000, 0},
	}

	for _, d := range defaults {
		_, _ = service.pool.Exec(ctx, `
			INSERT INTO benefit_plans (organization_id, name, category, description, employer_cost_minor, employee_cost_minor, currency)
			VALUES ($1, $2, $3, $4, $5, $6, 'USD')
		`, organizationID, d.name, d.category, d.desc, d.employer, d.employee)
	}
	return nil
}

func (service *Service) CreatePlan(ctx context.Context, organizationID, actorUserID string, input CreatePlanInput) (BenefitPlan, error) {
	if input.Currency == "" {
		input.Currency = "USD"
	}
	if input.Category == "" {
		input.Category = "other"
	}

	var item BenefitPlan
	err := service.pool.QueryRow(ctx, `
		INSERT INTO benefit_plans (organization_id, name, category, description, employer_cost_minor, employee_cost_minor, currency, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text, name, category, description, employer_cost_minor, employee_cost_minor, currency
	`, organizationID, input.Name, input.Category, input.Description, input.EmployerCostMinor, input.EmployeeCostMinor, input.Currency, actorUserID).Scan(
		&item.ID, &item.Name, &item.Category, &item.Description, &item.EmployerCostMinor, &item.EmployeeCostMinor, &item.Currency,
	)
	if err != nil {
		return BenefitPlan{}, err
	}
	return item, nil
}
