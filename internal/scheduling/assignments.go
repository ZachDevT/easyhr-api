package scheduling

import (
	"context"
)

type ShiftAssignment struct {
	ID            string  `json:"id"`
	EmployeeID    string  `json:"employeeId"`
	ScheduleID    string  `json:"scheduleId"`
	EffectiveFrom string  `json:"effectiveFrom"`
	EffectiveTo   *string `json:"effectiveTo"`
	EmployeeName  string  `json:"employeeName,omitempty"`
	ScheduleName  string  `json:"scheduleName,omitempty"`
}

type CreateAssignmentInput struct {
	ScheduleID    string  `json:"scheduleId"`
	EffectiveFrom string  `json:"effectiveFrom"`
	EffectiveTo   *string `json:"effectiveTo"`
}

func (service *Service) ListAssignments(ctx context.Context, organizationID, employeeID string) ([]ShiftAssignment, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT a.id::text, a.employee_id::text, a.schedule_id::text, a.effective_from::text, a.effective_to::text, e.first_name||' '||e.last_name, s.name
		FROM shift_assignments a JOIN employees e ON e.id=a.employee_id JOIN shift_schedules s ON s.id=a.schedule_id
		WHERE a.organization_id = $1 AND ($2='' OR a.employee_id::text = $2)
		ORDER BY a.effective_from DESC
	`, organizationID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []ShiftAssignment{}
	for rows.Next() {
		var item ShiftAssignment
		if err := rows.Scan(&item.ID, &item.EmployeeID, &item.ScheduleID, &item.EffectiveFrom, &item.EffectiveTo, &item.EmployeeName, &item.ScheduleName); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) CreateAssignment(ctx context.Context, organizationID, actorUserID, employeeID string, input CreateAssignmentInput) (ShiftAssignment, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return ShiftAssignment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify employee
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, employeeID, organizationID).Scan(&exists)
	if err != nil || !exists {
		return ShiftAssignment{}, ErrNotFound
	}

	// Verify schedule
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shift_schedules WHERE id = $1 AND organization_id = $2)`, input.ScheduleID, organizationID).Scan(&exists)
	if err != nil || !exists {
		return ShiftAssignment{}, ErrNotFound
	}

	_, err = tx.Exec(ctx, `UPDATE shift_assignments SET effective_to=$3::date-interval '1 day' WHERE organization_id=$1 AND employee_id=$2 AND effective_to IS NULL AND effective_from<$3::date`, organizationID, employeeID, input.EffectiveFrom)
	if err != nil {
		return ShiftAssignment{}, err
	}
	var item ShiftAssignment
	err = tx.QueryRow(ctx, `
		INSERT INTO shift_assignments (organization_id, employee_id, schedule_id, effective_from, effective_to, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, employee_id::text, schedule_id::text, effective_from::text, effective_to::text
	`, organizationID, employeeID, input.ScheduleID, input.EffectiveFrom, input.EffectiveTo, actorUserID).Scan(
		&item.ID, &item.EmployeeID, &item.ScheduleID, &item.EffectiveFrom, &item.EffectiveTo,
	)
	if err != nil {
		return ShiftAssignment{}, err
	}

	return item, tx.Commit(ctx)
}
