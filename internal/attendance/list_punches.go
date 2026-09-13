package attendance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

type ListPunchesInput struct {
	EmployeeID string
	From       string
	To         string
	Cursor     string
	Limit      int
}

type PunchPage struct {
	Data []Punch    `json:"data"`
	Page CursorPage `json:"page"`
}

type CursorPage struct {
	NextCursor *string `json:"nextCursor"`
	Limit      int     `json:"limit"`
	HasMore    bool    `json:"hasMore"`
}

type punchCursor struct {
	OccurredAt time.Time `json:"occurredAt"`
	ID         string    `json:"id"`
}

func (service *Service) ListPunches(ctx context.Context, organizationID string, input ListPunchesInput) (PunchPage, error) {
	if input.EmployeeID == "" {
		return PunchPage{}, fmt.Errorf("employeeId is required")
	}
	if input.Limit < 1 || input.Limit > 100 {
		return PunchPage{}, fmt.Errorf("limit must be between 1 and 100")
	}
	from, to, err := parseDateRange(input.From, input.To)
	if err != nil {
		return PunchPage{}, err
	}
	var cursor *punchCursor
	if input.Cursor != "" {
		parsed, err := decodePunchCursor(input.Cursor)
		if err != nil {
			return PunchPage{}, fmt.Errorf("cursor is invalid")
		}
		cursor = &parsed
	}
	var cursorAt *time.Time
	var cursorID any
	if cursor != nil {
		cursorAt, cursorID = &cursor.OccurredAt, cursor.ID
	}
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, employee_id::text, kind, occurred_at, source, policy_outcome, created_at
		FROM attendance_punches
		WHERE organization_id = $1 AND employee_id = $2
		  AND ($3::timestamptz IS NULL OR occurred_at >= $3)
		  AND ($4::timestamptz IS NULL OR occurred_at < $4)
		  AND ($5::timestamptz IS NULL OR (occurred_at, id) < ($5::timestamptz, $6::uuid))
		ORDER BY occurred_at DESC, id DESC
		LIMIT $7`, organizationID, input.EmployeeID, from, to, cursorAt, cursorID, input.Limit+1)
	if err != nil {
		return PunchPage{}, err
	}
	defer rows.Close()
	items := make([]Punch, 0, input.Limit+1)
	times := make([]time.Time, 0, input.Limit+1)
	for rows.Next() {
		var item Punch
		var occurredAt time.Time
		var createdAt time.Time
		if err = rows.Scan(&item.ID, &item.EmployeeID, &item.Kind, &occurredAt, &item.Source, &item.PolicyOutcome, &createdAt); err != nil {
			return PunchPage{}, err
		}
		item.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		items, times = append(items, item), append(times, occurredAt)
	}
	if err = rows.Err(); err != nil {
		return PunchPage{}, err
	}
	result := PunchPage{Data: items, Page: CursorPage{Limit: input.Limit}}
	if len(items) > input.Limit {
		result.Page.HasMore = true
		result.Data = items[:input.Limit]
		encoded, err := encodePunchCursor(punchCursor{OccurredAt: times[input.Limit-1], ID: result.Data[input.Limit-1].ID})
		if err != nil {
			return PunchPage{}, err
		}
		result.Page.NextCursor = &encoded
	}
	return result, nil
}

func parseDateRange(fromRaw, toRaw string) (*time.Time, *time.Time, error) {
	var from, to *time.Time
	if fromRaw != "" {
		value, err := time.Parse(time.DateOnly, fromRaw)
		if err != nil {
			return nil, nil, fmt.Errorf("from must be an ISO date")
		}
		from = &value
	}
	if toRaw != "" {
		value, err := time.Parse(time.DateOnly, toRaw)
		if err != nil {
			return nil, nil, fmt.Errorf("to must be an ISO date")
		}
		value = value.AddDate(0, 0, 1)
		to = &value
	}
	if from != nil && to != nil && !from.Before(*to) {
		return nil, nil, fmt.Errorf("from must be on or before to")
	}
	return from, to, nil
}

func encodePunchCursor(value punchCursor) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodePunchCursor(value string) (punchCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return punchCursor{}, err
	}
	var parsed punchCursor
	if err = json.Unmarshal(raw, &parsed); err != nil || parsed.ID == "" || parsed.OccurredAt.IsZero() {
		return punchCursor{}, fmt.Errorf("invalid cursor")
	}
	return parsed, nil
}
