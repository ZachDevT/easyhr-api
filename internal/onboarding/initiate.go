package onboarding

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrInitiationNotFound = errors.New("employee or published onboarding template not found")
var ErrInitiationConflict = errors.New("onboarding instance already exists")

type InitiateInput struct {
	EmployeeID string `json:"employeeId"`
	TemplateID string `json:"templateId"`
	StartDate  string `json:"startDate"`
}
type Instance struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employeeId"`
	TemplateID string `json:"templateId"`
	Status     string `json:"status"`
	TaskCount  int    `json:"taskCount"`
	StartDate  string `json:"startDate"`
}

func (input InitiateInput) Validate() error {
	if input.EmployeeID == "" || input.TemplateID == "" {
		return fmt.Errorf("employeeId and templateId are required")
	}
	if _, err := time.Parse(time.DateOnly, input.StartDate); err != nil {
		return fmt.Errorf("startDate must be an ISO date")
	}
	return nil
}
func (service *Service) Initiate(ctx context.Context, org, actor string, input InitiateInput) (Instance, error) {
	if err := input.Validate(); err != nil {
		return Instance{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Instance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var employeeExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id=$1 AND organization_id=$2 AND status IN ('onboarding','active'))`, input.EmployeeID, org).Scan(&employeeExists); err != nil {
		return Instance{}, err
	}
	if !employeeExists {
		return Instance{}, ErrInitiationNotFound
	}
	var version int
	err = tx.QueryRow(ctx, `SELECT version FROM onboarding_templates WHERE id=$1 AND organization_id=$2 AND status='published'`, input.TemplateID, org).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Instance{}, ErrInitiationNotFound
	}
	if err != nil {
		return Instance{}, err
	}
	result := Instance{EmployeeID: input.EmployeeID, TemplateID: input.TemplateID, Status: "not_started", StartDate: input.StartDate}
	err = tx.QueryRow(ctx, `INSERT INTO onboarding_instances (organization_id,employee_id,template_id,template_version,start_date,status,initiated_by_user_id) VALUES ($1,$2,$3,$4,$5,'not_started',$6) RETURNING id::text`, org, input.EmployeeID, input.TemplateID, version, input.StartDate, actor).Scan(&result.ID)
	if err != nil {
		if isUnique(err) {
			return Instance{}, ErrInitiationConflict
		}
		return Instance{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,title,description,category,assignee_type,due_offset_days,is_required,sort_order FROM onboarding_template_tasks WHERE template_id=$1 ORDER BY sort_order`, input.TemplateID)
	if err != nil {
		return Instance{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, title, category, assignee string
		var description *string
		var offset, order int
		var required bool
		if err = rows.Scan(&taskID, &title, &description, &category, &assignee, &offset, &required, &order); err != nil {
			return Instance{}, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO onboarding_tasks (onboarding_instance_id,source_template_task_id,title,description,category,assignee_type,due_date,is_required,sort_order) VALUES ($1,$2,$3,$4,$5,$6,$7::date+$8,$9,$10)`, result.ID, taskID, title, description, category, assignee, input.StartDate, offset, required, order); err != nil {
			return Instance{}, err
		}
		result.TaskCount++
	}
	if err = rows.Err(); err != nil {
		return Instance{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE onboarding_instances SET status=CASE WHEN EXISTS(SELECT 1 FROM onboarding_tasks WHERE onboarding_instance_id=$1) THEN 'on_track' ELSE 'completed' END,completed_at=CASE WHEN NOT EXISTS(SELECT 1 FROM onboarding_tasks WHERE onboarding_instance_id=$1) THEN now() ELSE NULL END WHERE id=$1`, result.ID); err != nil {
		return Instance{}, err
	}
	if result.TaskCount == 0 {
		result.Status = "completed"
	} else {
		result.Status = "on_track"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'onboarding.initiated','onboarding_instance',$3,'unknown',jsonb_build_object('taskCount',$4::int))`, org, actor, result.ID, result.TaskCount); err != nil {
		return Instance{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Instance{}, err
	}
	return result, nil
}
func isUnique(err error) bool {
	var pge interface{ SQLState() string }
	return errors.As(err, &pge) && pge.SQLState() == "23505"
}
