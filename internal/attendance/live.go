package attendance

import (
	"context"
	"time"
)

type LiveAttendanceInput struct {
	Date   string
	Status string
	Cursor string
	Limit  int
}

type LiveAttendance struct {
	EmployeeID     string  `json:"employeeId"`
	EmployeeNumber string  `json:"employeeNumber"`
	EmployeeName   string  `json:"employeeName"`
	Department     string  `json:"department"`
	JobTitle       string  `json:"jobTitle"`
	Avatar         string  `json:"avatar"`
	Date           string  `json:"date"`
	Status         string  `json:"status"` // "clocked_in" | "clocked_out" | "absent" | "late"
	ClockInAt      *string `json:"clockInAt"`
	ClockOutAt     *string `json:"clockOutAt"`
	LateMinutes    int     `json:"lateMinutes,omitempty"`
}

type LiveAttendancePage struct {
	Data []LiveAttendance `json:"data"`
	Page CursorPage       `json:"page"`
}

func (service *Service) GetLiveAttendance(ctx context.Context, organizationID string, input LiveAttendanceInput) (LiveAttendancePage, error) {
	if input.Limit <= 0 || input.Limit > 100 {
		input.Limit = 50
	}
	if input.Date == "" {
		input.Date = time.Now().UTC().Format("2006-01-02")
	}

	rows, err := service.pool.Query(ctx, `
		SELECT 
			e.id::text,
			e.employee_number,
			TRIM(CONCAT(e.first_name, ' ', e.last_name)) as full_name,
			COALESCE(d.name, 'General') as department,
			COALESCE(ea.job_title, 'Team Member') as job_title,
			COALESCE(u.avatar_url, '') as avatar,
			MIN(p_in.occurred_at)::text as clock_in,
			MAX(p_out.occurred_at)::text as clock_out
		FROM employees e
		LEFT JOIN users u ON u.id = e.user_id
		LEFT JOIN employment_assignments ea ON ea.employee_id = e.id AND ea.effective_to IS NULL
		LEFT JOIN departments d ON d.id = ea.department_id
		LEFT JOIN attendance_punches p_in 
			ON p_in.employee_id = e.id 
			AND p_in.kind = 'clock_in' 
			AND p_in.occurred_at >= $2::date
			AND p_in.occurred_at < ($2::date + INTERVAL '1 day')
		LEFT JOIN attendance_punches p_out 
			ON p_out.employee_id = e.id 
			AND p_out.kind = 'clock_out' 
			AND p_out.occurred_at >= $2::date
			AND p_out.occurred_at < ($2::date + INTERVAL '1 day')
		WHERE e.organization_id = $1 AND e.status IN ('active', 'onboarding')
		GROUP BY e.id, e.employee_number, e.first_name, e.last_name, d.name, ea.job_title, u.avatar_url
		ORDER BY full_name ASC
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
		if err := rows.Scan(
			&item.EmployeeID,
			&item.EmployeeNumber,
			&item.EmployeeName,
			&item.Department,
			&item.JobTitle,
			&item.Avatar,
			&clockIn,
			&clockOut,
		); err != nil {
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

		if input.Status != "" && item.Status != input.Status {
			continue
		}

		items = append(items, item)
	}

	return LiveAttendancePage{
		Data: items,
		Page: CursorPage{Limit: input.Limit, HasMore: false},
	}, nil
}
