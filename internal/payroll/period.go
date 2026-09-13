package payroll

import (
	"context"
)

type Period struct {
	ID        string `json:"id"`
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	PayDate   string `json:"payDate"`
	Status    string `json:"status"`
}

type CreatePeriodInput struct {
	StartDate string `json:"startDate"`
	EndDate   string `json:"endDate"`
	PayDate   string `json:"payDate"`
}

func (service *Service) ListPeriods(ctx context.Context, organizationID string) ([]Period, error) {
	rows, err := service.pool.Query(ctx, `SELECT id::text, start_date::text, end_date::text, pay_date::text, status FROM payroll_periods WHERE organization_id = $1 ORDER BY start_date DESC`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Period{}
	for rows.Next() {
		var item Period
		if err := rows.Scan(&item.ID, &item.StartDate, &item.EndDate, &item.PayDate, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) CreatePeriod(ctx context.Context, organizationID, actorUserID string, input CreatePeriodInput) (Period, error) {
	// basic validation could go here
	var p Period
	err := service.pool.QueryRow(ctx, `
		INSERT INTO payroll_periods (organization_id, start_date, end_date, pay_date, status, created_by_user_id)
		VALUES ($1, $2, $3, $4, 'draft', $5)
		RETURNING id::text, start_date::text, end_date::text, pay_date::text, status
	`, organizationID, input.StartDate, input.EndDate, input.PayDate, actorUserID).Scan(&p.ID, &p.StartDate, &p.EndDate, &p.PayDate, &p.Status)

	if err != nil {
		return Period{}, err
	}
	return p, nil
}
