package attendance

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrTimesheetNotFound = errors.New("timesheet not found or already reviewed")

type Timesheet struct {
	ID           string `json:"id"`
	EmployeeID   string `json:"employeeId"`
	EmployeeName string `json:"employeeName"`
	WeekStart    string `json:"weekStart"`
	WeekEnd      string `json:"weekEnd"`
	TotalMinutes int    `json:"totalMinutes"`
	Status       string `json:"status"`
	EmployeeNote string `json:"employeeNote"`
	ReviewerNote string `json:"reviewerNote"`
	SubmittedAt  string `json:"submittedAt"`
}

func (s *Service) SubmitTimesheet(ctx context.Context, org, userID, weekStart, note string) (Timesheet, error) {
	start, err := time.Parse("2006-01-02", weekStart)
	if err != nil {
		return Timesheet{}, err
	}
	if start.Weekday() != time.Monday {
		return Timesheet{}, errors.New("weekStart must be a Monday")
	}
	end := start.AddDate(0, 0, 6)
	var employeeID string
	if err = s.pool.QueryRow(ctx, `SELECT id::text FROM employees WHERE organization_id=$1 AND (user_id=$2 OR LOWER(work_email)=(SELECT LOWER(email) FROM users WHERE id=$2)) AND status IN ('active','leave','onboarding') LIMIT 1`, org, userID).Scan(&employeeID); err != nil {
		return Timesheet{}, err
	}
	var minutes int
	err = s.pool.QueryRow(ctx, `WITH ordered AS (SELECT kind,occurred_at,LEAD(kind) OVER (ORDER BY occurred_at) next_kind,LEAD(occurred_at) OVER (ORDER BY occurred_at) next_at FROM attendance_punches WHERE organization_id=$1 AND employee_id=$2 AND occurred_at >= $3 AND occurred_at < $4), pairs AS (SELECT EXTRACT(EPOCH FROM (next_at-occurred_at))/60 mins FROM ordered WHERE kind='clock_in' AND next_kind='clock_out') SELECT COALESCE(SUM(mins)::int,0) FROM pairs`, org, employeeID, start, end.AddDate(0, 0, 1)).Scan(&minutes)
	if err != nil {
		return Timesheet{}, err
	}
	var t Timesheet
	err = s.pool.QueryRow(ctx, `INSERT INTO timesheet_submissions (organization_id,employee_id,week_start,week_end,total_minutes,employee_note,status) VALUES ($1,$2,$3,$4,$5,$6,'submitted') ON CONFLICT (employee_id,week_start) DO UPDATE SET week_end=EXCLUDED.week_end,total_minutes=EXCLUDED.total_minutes,employee_note=EXCLUDED.employee_note,status='submitted',reviewer_note='',submitted_at=now(),reviewed_at=NULL,reviewed_by_user_id=NULL WHERE timesheet_submissions.status!='approved' RETURNING id::text,employee_id::text,week_start::text,week_end::text,total_minutes,status,employee_note,reviewer_note,submitted_at::text`, org, employeeID, start, end, minutes, note).Scan(&t.ID, &t.EmployeeID, &t.WeekStart, &t.WeekEnd, &t.TotalMinutes, &t.Status, &t.EmployeeNote, &t.ReviewerNote, &t.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Timesheet{}, errors.New("approved timesheet cannot be resubmitted")
	}
	return t, err
}

func (s *Service) ListTimesheets(ctx context.Context, org string) ([]Timesheet, error) {
	rows, err := s.pool.Query(ctx, `SELECT ts.id::text,ts.employee_id::text,e.first_name||' '||e.last_name,ts.week_start::text,ts.week_end::text,ts.total_minutes,ts.status,ts.employee_note,ts.reviewer_note,ts.submitted_at::text FROM timesheet_submissions ts JOIN employees e ON e.id=ts.employee_id WHERE ts.organization_id=$1 ORDER BY ts.week_start DESC,ts.submitted_at DESC LIMIT 250`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Timesheet{}
	for rows.Next() {
		var t Timesheet
		if err := rows.Scan(&t.ID, &t.EmployeeID, &t.EmployeeName, &t.WeekStart, &t.WeekEnd, &t.TotalMinutes, &t.Status, &t.EmployeeNote, &t.ReviewerNote, &t.SubmittedAt); err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func (s *Service) ReviewTimesheet(ctx context.Context, org, actor, id, decision, note, requestID, ip, ua string) error {
	if decision != "approved" && decision != "rejected" {
		return errors.New("decision must be approved or rejected")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE timesheet_submissions SET status=$3,reviewer_note=$4,reviewed_at=now(),reviewed_by_user_id=$5 WHERE id=$1 AND organization_id=$2 AND status='submitted'`, id, org, decision, note, actor)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrTimesheetNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,ip_address,user_agent,metadata) VALUES ($1,$2,'timesheet.'||$4,'timesheet_submission',$3,$5,NULLIF($6,'')::inet,$7,jsonb_build_object('note',$8::text))`, org, actor, id, decision, requestID, ip, ua, note); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
