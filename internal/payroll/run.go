package payroll

import (
	"context"
)

type Run struct {
	ID              string  `json:"id"`
	PayrollPeriodID string  `json:"payrollPeriodId"`
	Status          string  `json:"status"`
	ApprovedAt      *string `json:"approvedAt"`
}

func (service *Service) InitiateRun(ctx context.Context, organizationID, actorUserID, periodID string) (Run, error) {
	// verify period belongs to org
	var exists bool
	err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM payroll_periods WHERE id = $1 AND organization_id = $2)`, periodID, organizationID).Scan(&exists)
	if err != nil {
		return Run{}, err
	}
	if !exists {
		return Run{}, ErrNotFound
	}

	// Create run
	var r Run
	err = service.pool.QueryRow(ctx, `
		INSERT INTO payroll_runs (payroll_period_id, status, created_by_user_id)
		VALUES ($1, 'calculated', $2)
		RETURNING id::text, payroll_period_id::text, status
	`, periodID, actorUserID).Scan(&r.ID, &r.PayrollPeriodID, &r.Status)
	if err != nil {
		return Run{}, err
	}

	// Normally we would invoke the engine here for all eligible employees and write payroll_run_items.
	// For the MVP, we just create the run.
	// The next step in a real system is dispatching a background job to process the items.

	return r, nil
}

func (service *Service) LockRun(ctx context.Context, organizationID, actorUserID, runID string) error {
	// lock and generate paystubs
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res, err := tx.Exec(ctx, `
		UPDATE payroll_runs 
		SET status = 'approved', approved_at = now(), approved_by_user_id = $1
		WHERE id = $2 AND status IN ('draft', 'calculated') 
		  AND EXISTS (
			SELECT 1 FROM payroll_periods pp WHERE pp.id = payroll_runs.payroll_period_id AND pp.organization_id = $3
		  )
	`, actorUserID, runID, organizationID)

	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrInvalidState
	}

	// Mark period as locked
	_, err = tx.Exec(ctx, `
		UPDATE payroll_periods pp
		SET status = 'locked'
		FROM payroll_runs pr
		WHERE pr.payroll_period_id = pp.id AND pr.id = $1
	`, runID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}
