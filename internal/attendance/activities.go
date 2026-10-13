package attendance

import (
	"context"
	"fmt"
)

type Activity struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	Name           string `json:"name"`
	Code           string `json:"code"`
	IsBillable     bool   `json:"isBillable"`
	IsDefault      bool   `json:"isDefault"`
	Color          string `json:"color"`
	CreatedAt      string `json:"createdAt"`
}

type CreateActivityInput struct {
	Name       string `json:"name"`
	Code       string `json:"code,omitempty"`
	IsBillable bool   `json:"isBillable"`
	IsDefault  bool   `json:"isDefault"`
	Color      string `json:"color,omitempty"`
}

var defaultActivities = []struct {
	Name       string
	Code       string
	IsBillable bool
	IsDefault  bool
	Color      string
}{
	{"Production", "prod", true, true, "#7c3aed"},
	{"Meeting", "meet", true, false, "#2563eb"},
	{"Break", "brk", false, false, "#f59e0b"},
	{"Lunch", "lnch", false, false, "#10b981"},
	{"Training", "trng", true, false, "#8b5cf6"},
	{"Client Work", "clnt", true, false, "#06b6d4"},
}

func (service *Service) ListActivities(ctx context.Context, orgID string) ([]Activity, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, organization_id::text, name, code, is_billable, is_default, color, created_at::text
		FROM attendance_activities
		WHERE organization_id = $1
		ORDER BY is_default DESC, name ASC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.Name, &a.Code, &a.IsBillable, &a.IsDefault, &a.Color, &a.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, a)
	}

	if len(list) == 0 {
		for _, da := range defaultActivities {
			var a Activity
			err := service.pool.QueryRow(ctx, `
				INSERT INTO attendance_activities (organization_id, name, code, is_billable, is_default, color)
				VALUES ($1, $2, $3, $4, $5, $6)
				ON CONFLICT (organization_id, name) DO NOTHING
				RETURNING id::text, organization_id::text, name, code, is_billable, is_default, color, created_at::text
			`, orgID, da.Name, da.Code, da.IsBillable, da.IsDefault, da.Color).Scan(
				&a.ID, &a.OrganizationID, &a.Name, &a.Code, &a.IsBillable, &a.IsDefault, &a.Color, &a.CreatedAt,
			)
			if err == nil {
				list = append(list, a)
			}
		}
	}

	return list, nil
}

func (service *Service) CreateActivity(ctx context.Context, orgID string, input CreateActivityInput) (Activity, error) {
	if input.Name == "" {
		return Activity{}, fmt.Errorf("activity name is required")
	}
	if input.Code == "" {
		input.Code = input.Name
	}
	if input.Color == "" {
		input.Color = "#7c3aed"
	}

	var a Activity
	err := service.pool.QueryRow(ctx, `
		INSERT INTO attendance_activities (organization_id, name, code, is_billable, is_default, color)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (organization_id, name) DO UPDATE SET 
			is_billable = EXCLUDED.is_billable,
			is_default = EXCLUDED.is_default,
			color = EXCLUDED.color
		RETURNING id::text, organization_id::text, name, code, is_billable, is_default, color, created_at::text
	`, orgID, input.Name, input.Code, input.IsBillable, input.IsDefault, input.Color).Scan(
		&a.ID, &a.OrganizationID, &a.Name, &a.Code, &a.IsBillable, &a.IsDefault, &a.Color, &a.CreatedAt,
	)
	return a, err
}

func (service *Service) DeleteActivity(ctx context.Context, orgID, activityID string) error {
	_, err := service.pool.Exec(ctx, `DELETE FROM attendance_activities WHERE organization_id = $1 AND id = $2`, orgID, activityID)
	return err
}
