package promotions

import (
	"context"
	"github.com/jackc/pgx/v5"
)

type PromotionEvent struct {
	ID                        string  `json:"id"`
	EmployeeID                string  `json:"employeeId"`
	EmployeeName              string  `json:"employeeName,omitempty"`
	EffectiveDate             string  `json:"effectiveDate"`
	PreviousJobTitle          *string `json:"previousJobTitle"`
	NewJobTitle               string  `json:"newJobTitle"`
	PreviousManagerID         *string `json:"previousManagerId"`
	NewManagerID              *string `json:"newManagerId"`
	PreviousCompensationMinor *int64  `json:"previousCompensationMinor"`
	NewCompensationMinor      *int64  `json:"newCompensationMinor"`
	Rationale                 *string `json:"rationale"`
	Status                    string  `json:"status"`
}

type ExecutePromotionInput struct {
	EffectiveDate        string  `json:"effectiveDate"`
	NewJobTitle          string  `json:"newJobTitle"`
	PositionID           string  `json:"positionId"`
	NewManagerID         *string `json:"newManagerId"`
	NewCompensationMinor *int64  `json:"newCompensationMinor"`
	Rationale            *string `json:"rationale"`
}

func (service *Service) ListPromotions(ctx context.Context, organizationID, employeeID string) ([]PromotionEvent, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT p.id::text, p.employee_id::text, COALESCE(e.first_name || ' ' || e.last_name, 'Team Member') as employee_name,
		       p.effective_date::text, p.previous_job_title, p.new_job_title, 
		       p.previous_manager_id::text, p.new_manager_id::text, p.previous_compensation_minor, p.new_compensation_minor, p.rationale, p.status 
		FROM promotion_events p
		LEFT JOIN employees e ON e.id = p.employee_id
		WHERE p.organization_id = $1 AND ($2 = '' OR p.employee_id::text = $2)
		ORDER BY p.created_at DESC
	`, organizationID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []PromotionEvent{}
	for rows.Next() {
		var item PromotionEvent
		if err := rows.Scan(&item.ID, &item.EmployeeID, &item.EmployeeName, &item.EffectiveDate, &item.PreviousJobTitle, &item.NewJobTitle,
			&item.PreviousManagerID, &item.NewManagerID, &item.PreviousCompensationMinor, &item.NewCompensationMinor,
			&item.Rationale, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) ExecutePromotion(ctx context.Context, organizationID, actorUserID, employeeID string, input ExecutePromotionInput) (PromotionEvent, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return PromotionEvent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify employee
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, employeeID, organizationID).Scan(&exists)
	if err != nil || !exists {
		return PromotionEvent{}, ErrNotFound
	}

	var newJobTitle string
	if input.PositionID == "" {
		return PromotionEvent{}, ErrNotFound
	}
	var targetDepartment string
	if err := tx.QueryRow(ctx, `SELECT title,department_id::text FROM department_positions WHERE id=$1 AND organization_id=$2 AND status='active'`, input.PositionID, organizationID).Scan(&newJobTitle, &targetDepartment); err != nil {
		return PromotionEvent{}, ErrNotFound
	}
	var employeeDepartment *string
	_ = tx.QueryRow(ctx, `SELECT department_id::text FROM employment_assignments WHERE employee_id=$1 AND effective_to IS NULL ORDER BY effective_from DESC LIMIT 1`, employeeID).Scan(&employeeDepartment)
	if employeeDepartment == nil {
		var targetExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM departments WHERE id=$1 AND organization_id=$2)`, targetDepartment, organizationID).Scan(&targetExists); err != nil || !targetExists {
			return PromotionEvent{}, ErrNotFound
		}
	}
	input.NewJobTitle = newJobTitle

	// 1. Get current assignment
	var prevTitle *string
	var prevManager *string
	var prevDept, prevDiv, prevLoc *string
	err = tx.QueryRow(ctx, `
		SELECT job_title, manager_employee_id::text, department_id::text, division_id::text, location_id::text 
		FROM employment_assignments 
		WHERE employee_id = $1 AND effective_to IS NULL 
		ORDER BY effective_from DESC LIMIT 1
	`, employeeID).Scan(&prevTitle, &prevManager, &prevDept, &prevDiv, &prevLoc)
	if err != nil && err != pgx.ErrNoRows {
		return PromotionEvent{}, err
	}

	// 2. Get current compensation (if payroll is active)
	var prevComp *int64
	var prevCurrency, prevPayType *string
	err = tx.QueryRow(ctx, `
		SELECT base_amount_minor, currency, pay_type 
		FROM compensation_records 
		WHERE employee_id = $1 AND effective_to IS NULL 
		ORDER BY effective_from DESC LIMIT 1
	`, employeeID).Scan(&prevComp, &prevCurrency, &prevPayType)
	if err != nil && err != pgx.ErrNoRows {
		return PromotionEvent{}, err
	}

	// Close old assignment
	if err != pgx.ErrNoRows {
		_, err = tx.Exec(ctx, `UPDATE employment_assignments SET effective_to = $2::date - interval '1 day' WHERE employee_id = $1 AND effective_to IS NULL`, employeeID, input.EffectiveDate)
		if err != nil {
			return PromotionEvent{}, err
		}
	}

	// Insert new assignment
	managerToInsert := prevManager
	prevDept = &targetDepartment
	if input.NewManagerID != nil {
		managerToInsert = input.NewManagerID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO employment_assignments (employee_id, department_id, division_id, location_id, manager_employee_id, job_title, effective_from, created_by_user_id,position_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8,$9)
	`, employeeID, prevDept, prevDiv, prevLoc, managerToInsert, input.NewJobTitle, input.EffectiveDate, actorUserID, input.PositionID)
	if err != nil {
		return PromotionEvent{}, err
	}

	// Compensation change
	if input.NewCompensationMinor != nil && prevCurrency != nil && prevPayType != nil {
		// Close old compensation
		_, err = tx.Exec(ctx, `UPDATE compensation_records SET effective_to = $2::date - interval '1 day' WHERE employee_id = $1 AND effective_to IS NULL`, employeeID, input.EffectiveDate)
		if err != nil {
			return PromotionEvent{}, err
		}
		// Insert new compensation
		_, err = tx.Exec(ctx, `
			INSERT INTO compensation_records (employee_id, pay_type, base_amount_minor, currency, effective_from, created_by_user_id)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, employeeID, prevPayType, input.NewCompensationMinor, prevCurrency, input.EffectiveDate, actorUserID)
		if err != nil {
			return PromotionEvent{}, err
		}
	}

	// 3. Insert audit log
	var event PromotionEvent
	err = tx.QueryRow(ctx, `
		INSERT INTO promotion_events (organization_id, employee_id, effective_date, previous_job_title, new_job_title, previous_manager_id, new_manager_id, previous_compensation_minor, new_compensation_minor, rationale, created_by_user_id,new_position_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11,$12)
		RETURNING id::text, employee_id::text, effective_date::text, previous_job_title, new_job_title, previous_manager_id::text, new_manager_id::text, previous_compensation_minor, new_compensation_minor, rationale, status
	`, organizationID, employeeID, input.EffectiveDate, prevTitle, input.NewJobTitle, prevManager, input.NewManagerID, prevComp, input.NewCompensationMinor, input.Rationale, actorUserID, input.PositionID).Scan(
		&event.ID, &event.EmployeeID, &event.EffectiveDate, &event.PreviousJobTitle, &event.NewJobTitle, &event.PreviousManagerID, &event.NewManagerID, &event.PreviousCompensationMinor, &event.NewCompensationMinor, &event.Rationale, &event.Status,
	)
	if err != nil {
		return PromotionEvent{}, err
	}

	return event, tx.Commit(ctx)
}
