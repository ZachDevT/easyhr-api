package payroll

import (
	"context"
	"fmt"
	"strings"
)

type Configuration struct {
	ID                     string `json:"id"`
	Country                string `json:"country"`
	Currency               string `json:"currency"`
	StandardMonthlyMinutes int    `json:"standardMonthlyMinutes"`
	OrganizationID         string `json:"-"`
}

type CreateConfigurationInput struct {
	Country                string `json:"country"`
	Currency               string `json:"currency"`
	StandardMonthlyMinutes int    `json:"standardMonthlyMinutes"`
}

type TaxRule struct {
	ID          string    `json:"id"`
	Country     string    `json:"country"`
	Currency    string    `json:"currency"`
	RuleName    string    `json:"ruleName"`
	RuleType    string    `json:"ruleType"`
	Bands       []TaxBand `json:"bands"`
	RatePercent float64   `json:"ratePercent"`
	Active      bool      `json:"active"`
}

type TaxRuleInput struct {
	Country     string    `json:"country"`
	Currency    string    `json:"currency"`
	RuleName    string    `json:"ruleName"`
	RuleType    string    `json:"ruleType"`
	Bands       []TaxBand `json:"bands"`
	RatePercent float64   `json:"ratePercent"`
}

func (service *Service) CreateConfiguration(ctx context.Context, organizationID string, input CreateConfigurationInput) (Configuration, error) {
	input.Country = strings.ToUpper(strings.TrimSpace(input.Country))
	input.Currency = strings.ToUpper(strings.TrimSpace(input.Currency))
	if len(input.Country) != 2 {
		return Configuration{}, fmt.Errorf("country must be an ISO 3166-1 alpha-2 code")
	}
	if len(input.Currency) != 3 {
		return Configuration{}, fmt.Errorf("currency must be a three-letter code")
	}
	if input.StandardMonthlyMinutes <= 0 {
		return Configuration{}, fmt.Errorf("standardMonthlyMinutes must be positive")
	}

	var config Configuration
	err := service.pool.QueryRow(ctx, `
		INSERT INTO payroll_configurations (organization_id, country, currency, standard_monthly_minutes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id, country) DO UPDATE 
		SET currency = EXCLUDED.currency, standard_monthly_minutes = EXCLUDED.standard_monthly_minutes
		RETURNING id::text, country, currency, standard_monthly_minutes
	`, organizationID, input.Country, input.Currency, input.StandardMonthlyMinutes).Scan(
		&config.ID, &config.Country, &config.Currency, &config.StandardMonthlyMinutes,
	)
	return config, err
}

func (service *Service) ListConfigurations(ctx context.Context, organizationID string) ([]Configuration, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text, country, currency, standard_monthly_minutes FROM payroll_configurations WHERE organization_id = $1 ORDER BY country`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Configuration{}
	for rows.Next() {
		var item Configuration
		if err := rows.Scan(&item.ID, &item.Country, &item.Currency, &item.StandardMonthlyMinutes); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
