package benefits

import (
	"context"
	"fmt"
	"strings"
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
	var eligibleCount int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM employees WHERE organization_id = $1`, organizationID).Scan(&eligibleCount); err != nil {
		return nil, err
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

func (service *Service) CreatePlan(ctx context.Context, organizationID, actorUserID string, input CreatePlanInput) (BenefitPlan, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if input.Category == "" {
		input.Category = "other"
	}
	if input.Name == "" || len(input.Name) > 255 {
		return BenefitPlan{}, fmt.Errorf("name must contain between 1 and 255 characters")
	}
	if len(input.Currency) != 3 {
		return BenefitPlan{}, fmt.Errorf("currency must be a three-letter code")
	}
	if input.EmployerCostMinor < 0 || input.EmployeeCostMinor < 0 {
		return BenefitPlan{}, fmt.Errorf("benefit costs cannot be negative")
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
