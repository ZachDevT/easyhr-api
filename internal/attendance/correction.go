package attendance

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrPunchNotFound = errors.New("attendance punch not found")

type CreateCorrectionInput struct {
	OccurredAt string `json:"occurredAt"`
	Reason     string `json:"reason"`
}
type Correction struct {
	ID                    string `json:"id"`
	OriginalPunchID       string `json:"originalPunchId"`
	ReplacementOccurredAt string `json:"replacementOccurredAt"`
	Status                string `json:"status"`
}

func (input CreateCorrectionInput) Validate() error {
	if _, err := time.Parse(time.RFC3339, input.OccurredAt); err != nil {
		return fmt.Errorf("occurredAt must be RFC 3339")
	}
	if n := len(input.Reason); n < 1 || n > 2000 {
		return fmt.Errorf("reason must contain between 1 and 2000 characters")
	}
	return nil
}
func (service *Service) CreateCorrection(ctx context.Context, org, actor, punchID string, input CreateCorrectionInput) (Correction, error) {
	if err := input.Validate(); err != nil {
		return Correction{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Correction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM attendance_punches WHERE id=$1 AND organization_id=$2)`, punchID, org).Scan(&exists)
	if err != nil {
		return Correction{}, err
	}
	if !exists {
		return Correction{}, ErrPunchNotFound
	}
	result := Correction{OriginalPunchID: punchID, ReplacementOccurredAt: input.OccurredAt, Status: "applied"}
	err = tx.QueryRow(ctx, `INSERT INTO attendance_corrections (organization_id,original_punch_id,replacement_occurred_at,reason,created_by_user_id) VALUES ($1,$2,$3,$4,$5) RETURNING id::text`, org, punchID, input.OccurredAt, input.Reason, actor).Scan(&result.ID)
	if err != nil {
		return Correction{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'attendance.punch_corrected','attendance_correction',$3,'unknown',jsonb_build_object('originalPunchId',$4::text))`, org, actor, result.ID, punchID); err != nil {
		return Correction{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Correction{}, err
	}
	return result, nil
}
