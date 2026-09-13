package holidays

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

type Holiday struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	HolidayDate string `json:"holidayDate"`
	HolidayType string `json:"holidayType"`
	AppliesTo   string `json:"appliesTo"`
}

type CreateHolidayInput struct {
	Name        string  `json:"name"`
	HolidayDate string  `json:"holidayDate"`
	HolidayType string  `json:"holidayType"`
	AppliesTo   *string `json:"appliesTo"`
}

func (s *Service) ListHolidays(ctx context.Context, orgID string) ([]Holiday, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, name, holiday_date::text, holiday_type, applies_to FROM company_holidays WHERE organization_id = $1 ORDER BY holiday_date ASC", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Holiday
	for rows.Next() {
		var h Holiday
		if err := rows.Scan(&h.ID, &h.Name, &h.HolidayDate, &h.HolidayType, &h.AppliesTo); err != nil {
			return nil, err
		}
		items = append(items, h)
	}
	return items, nil
}

func (s *Service) CreateHoliday(ctx context.Context, orgID, userID string, input CreateHolidayInput) (Holiday, error) {
	var h Holiday
	applies := "all_employees"
	if input.AppliesTo != nil && *input.AppliesTo != "" {
		applies = *input.AppliesTo
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO company_holidays (organization_id, name, holiday_date, holiday_type, applies_to, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, name, holiday_date::text, holiday_type, applies_to
	`, orgID, input.Name, input.HolidayDate, input.HolidayType, applies, userID).Scan(
		&h.ID, &h.Name, &h.HolidayDate, &h.HolidayType, &h.AppliesTo,
	)
	return h, err
}
