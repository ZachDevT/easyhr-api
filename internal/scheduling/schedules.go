package scheduling

import (
	"context"
)

type ShiftEntry struct {
	DayOfWeek    int    `json:"dayOfWeek"`
	StartTime    string `json:"startTime"`
	EndTime      string `json:"endTime"`
	BreakMinutes int    `json:"breakMinutes"`
}

type ShiftSchedule struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Timezone    string       `json:"timezone"`
	Description *string      `json:"description"`
	Entries     []ShiftEntry `json:"entries"`
}

type CreateScheduleInput struct {
	Name        string       `json:"name"`
	Timezone    string       `json:"timezone"`
	Description *string      `json:"description"`
	Entries     []ShiftEntry `json:"entries"`
}

func (service *Service) ListSchedules(ctx context.Context, organizationID string) ([]ShiftSchedule, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, name, timezone, description 
		FROM shift_schedules 
		WHERE organization_id = $1 
		ORDER BY created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ShiftSchedule{}
	for rows.Next() {
		var item ShiftSchedule
		if err := rows.Scan(&item.ID, &item.Name, &item.Timezone, &item.Description); err != nil {
			return nil, err
		}
		item.Entries = []ShiftEntry{}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range items {
		entries, err := service.pool.Query(ctx, `SELECT day_of_week,start_time::text,end_time::text,break_minutes FROM shift_entries WHERE schedule_id=$1 ORDER BY day_of_week`, items[i].ID)
		if err != nil {
			return nil, err
		}
		for entries.Next() {
			var entry ShiftEntry
			if err := entries.Scan(&entry.DayOfWeek, &entry.StartTime, &entry.EndTime, &entry.BreakMinutes); err != nil {
				entries.Close()
				return nil, err
			}
			items[i].Entries = append(items[i].Entries, entry)
		}
		if err := entries.Err(); err != nil {
			entries.Close()
			return nil, err
		}
		entries.Close()
	}
	return items, nil
}

func (service *Service) CreateSchedule(ctx context.Context, organizationID, actorUserID string, input CreateScheduleInput) (ShiftSchedule, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return ShiftSchedule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var item ShiftSchedule
	err = tx.QueryRow(ctx, `
		INSERT INTO shift_schedules (organization_id, name, timezone, description, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, name, timezone, description
	`, organizationID, input.Name, input.Timezone, input.Description, actorUserID).Scan(
		&item.ID, &item.Name, &item.Timezone, &item.Description,
	)
	if err != nil {
		return ShiftSchedule{}, err
	}

	for _, entry := range input.Entries {
		_, err = tx.Exec(ctx, `
			INSERT INTO shift_entries (schedule_id, day_of_week, start_time, end_time, break_minutes)
			VALUES ($1, $2, $3, $4, $5)
		`, item.ID, entry.DayOfWeek, entry.StartTime, entry.EndTime, entry.BreakMinutes)
		if err != nil {
			return ShiftSchedule{}, err
		}
		item.Entries = append(item.Entries, entry)
	}

	if item.Entries == nil {
		item.Entries = []ShiftEntry{}
	}

	return item, tx.Commit(ctx)
}
