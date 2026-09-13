package attendance

import (
	"context"
)

type LiveAttendanceInput struct {
	Date   string
	Status string
	Cursor string
	Limit  int
}

type LiveAttendance struct {
	EmployeeID  string  `json:"employeeId"`
	Date        string  `json:"date"`
	Status      string  `json:"status"`
	ClockInAt   *string `json:"clockInAt"`
	ClockOutAt  *string `json:"clockOutAt"`
	LateMinutes int     `json:"lateMinutes,omitempty"`
}

type LiveAttendancePage struct {
	Data []LiveAttendance `json:"data"`
	Page CursorPage       `json:"page"`
}

func (service *Service) GetLiveAttendance(ctx context.Context, organizationID string, input LiveAttendanceInput) (LiveAttendancePage, error) {
	if input.Limit <= 0 || input.Limit > 100 {
		input.Limit = 50
	}

	// Query the first clock_in and last clock_out for each employee on the given date
	rows, err := service.pool.Query(ctx, `
		SELECT 
			e.id::text,
			MIN(p_in.occurred_at)::text as clock_in,
			MAX(p_out.occurred_at)::text as clock_out
		FROM employees e
		LEFT JOIN attendance_punches p_in 
			ON p_in.employee_id = e.id 
			AND p_in.kind = 'clock_in' 
			AND DATE(p_in.occurred_at AT TIME ZONE 'UTC') = $2
		LEFT JOIN attendance_punches p_out 
			ON p_out.employee_id = e.id 
			AND p_out.kind = 'clock_out' 
			AND DATE(p_out.occurred_at AT TIME ZONE 'UTC') = $2
		WHERE e.organization_id = $1 AND e.status IN ('active', 'onboarding')
		GROUP BY e.id
		ORDER BY e.id
		LIMIT $3
	`, organizationID, input.Date, input.Limit)

	if err != nil {
		return LiveAttendancePage{}, err
	}
	defer rows.Close()

	items := []LiveAttendance{}
	for rows.Next() {
		var item LiveAttendance
		item.Date = input.Date
		var clockIn, clockOut *string
		if err := rows.Scan(&item.EmployeeID, &clockIn, &clockOut); err != nil {
			return LiveAttendancePage{}, err
		}

		item.ClockInAt = clockIn
		item.ClockOutAt = clockOut

		if item.ClockInAt != nil && item.ClockOutAt == nil {
			item.Status = "clocked_in"
		} else if item.ClockInAt != nil && item.ClockOutAt != nil {
			item.Status = "clocked_out"
		} else {
			item.Status = "absent"
		}

		// Optional: filter by requested status
		if input.Status != "" && item.Status != input.Status {
			continue
		}

		items = append(items, item)
	}

	return LiveAttendancePage{
		Data: items,
		Page: CursorPage{Limit: input.Limit, HasMore: false}, // Simplification for now
	}, nil
}
