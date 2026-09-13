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
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, name, category, description, employer_cost_minor, employee_cost_minor, currency 
		FROM benefit_plans 
		WHERE organization_id = $1 
		ORDER BY created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []BenefitPlan{}
	for rows.Next() {
		var item BenefitPlan
		if err := rows.Scan(&item.ID, &item.Name, &item.Category, &item.Description, &item.EmployerCostMinor, &item.EmployeeCostMinor, &item.Currency); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) CreatePlan(ctx context.Context, organizationID, actorUserID string, input CreatePlanInput) (BenefitPlan, error) {
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
