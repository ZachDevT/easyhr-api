package payroll

import (
	"context"
)

type Configuration struct {
	ID                     string `json:"id"`
	Country                string `json:"country"`
	Currency               string `json:"currency"`
	StandardMonthlyMinutes int    `json:"standardMonthlyMinutes"`
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
