package payroll

import (
	"context"
	"fmt"
	"strings"
)

type Configuration struct {
	ID                     string  `json:"id"`
	Country                string  `json:"country"`
	Currency               string  `json:"currency"`
	StandardMonthlyMinutes int     `json:"standardMonthlyMinutes"`
	OvertimeMultiplier     float64 `json:"overtimeMultiplier"`
	HolidayMultiplier      float64 `json:"holidayMultiplier"`
	OrganizationID         string  `json:"-"`
}

type CreateConfigurationInput struct {
	Country                string   `json:"country"`
	Currency               string   `json:"currency"`
	StandardMonthlyMinutes int      `json:"standardMonthlyMinutes"`
	OvertimeMultiplier     *float64 `json:"overtimeMultiplier,omitempty"`
	HolidayMultiplier      *float64 `json:"holidayMultiplier,omitempty"`
}

type TaxRule struct {
	ID          string    `json:"id"`
	Country     string    `json:"country"`
	Currency    string    `json:"currency"`
	RuleName    string    `json:"ruleName"`
	RuleType    string    `json:"ruleType"`
	Bands       []TaxBand `json:"bands"`
	RatePercent float64   `json:"ratePercent"`
	AmountMinor int64     `json:"amountMinor"`
	AppliesTo   string    `json:"appliesTo"`
	AppliesToID string    `json:"appliesToId,omitempty"`
	Cadence     string    `json:"cadence"`
	Active      bool      `json:"active"`
}

type TaxRuleInput struct {
	Country     string    `json:"country"`
	Currency    string    `json:"currency"`
	RuleName    string    `json:"ruleName"`
	RuleType    string    `json:"ruleType"`
	Bands       []TaxBand `json:"bands"`
	RatePercent float64   `json:"ratePercent"`
	AmountMinor int64     `json:"amountMinor"`
	AppliesTo   string    `json:"appliesTo"`
	AppliesToID string    `json:"appliesToId,omitempty"`
	Cadence     string    `json:"cadence"`
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

	otMultiplier := 1.50
	if input.OvertimeMultiplier != nil && *input.OvertimeMultiplier > 0 {
		otMultiplier = *input.OvertimeMultiplier
	}
	holMultiplier := 2.00
	if input.HolidayMultiplier != nil && *input.HolidayMultiplier > 0 {
		holMultiplier = *input.HolidayMultiplier
	}

	var config Configuration
	err := service.pool.QueryRow(ctx, `
		INSERT INTO payroll_configurations (organization_id, country, currency, standard_monthly_minutes, overtime_multiplier, holiday_multiplier)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (organization_id, country) DO UPDATE 
		SET currency = EXCLUDED.currency, 
		    standard_monthly_minutes = EXCLUDED.standard_monthly_minutes,
		    overtime_multiplier = EXCLUDED.overtime_multiplier,
		    holiday_multiplier = EXCLUDED.holiday_multiplier
		RETURNING id::text, country, currency, standard_monthly_minutes, overtime_multiplier::float8, holiday_multiplier::float8
	`, organizationID, input.Country, input.Currency, input.StandardMonthlyMinutes, otMultiplier, holMultiplier).Scan(
		&config.ID, &config.Country, &config.Currency, &config.StandardMonthlyMinutes, &config.OvertimeMultiplier, &config.HolidayMultiplier,
	)
	return config, err
}

func (service *Service) ListConfigurations(ctx context.Context, organizationID string) ([]Configuration, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text, country, currency, standard_monthly_minutes, COALESCE(overtime_multiplier, 1.50)::float8, COALESCE(holiday_multiplier, 2.00)::float8 FROM payroll_configurations WHERE organization_id = $1 ORDER BY country`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Configuration{}
	for rows.Next() {
		var item Configuration
		if err := rows.Scan(&item.ID, &item.Country, &item.Currency, &item.StandardMonthlyMinutes, &item.OvertimeMultiplier, &item.HolidayMultiplier); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}
