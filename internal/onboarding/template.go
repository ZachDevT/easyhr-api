package onboarding

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type TemplateTaskInput struct {
	Title         string `json:"title"`
	Description   string `json:"description,omitempty"`
	Category      string `json:"category"`
	AssigneeType  string `json:"assigneeType"`
	DueOffsetDays int    `json:"dueOffsetDays"`
	IsRequired    bool   `json:"isRequired"`
	SortOrder     int    `json:"sortOrder"`
}
type CreateTemplateInput struct {
	Name  string              `json:"name"`
	Tasks []TemplateTaskInput `json:"tasks"`
}
type Template struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Version   int    `json:"version"`
	Status    string `json:"status"`
	TaskCount int    `json:"taskCount"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (service *Service) ListTemplates(ctx context.Context, organizationID, status string) ([]Template, error) {
	if status != "" && !oneOf(status, "draft", "published", "archived") {
		return nil, fmt.Errorf("status must be draft, published, or archived")
	}
	rows, err := service.pool.Query(ctx, `
		SELECT template.id::text, template.name, template.version, template.status, count(task.id)
		FROM onboarding_templates template
		LEFT JOIN onboarding_template_tasks task ON task.template_id = template.id
		WHERE template.organization_id = $1 AND ($2 = '' OR template.status = $2)
		GROUP BY template.id
		ORDER BY template.name ASC, template.version DESC`, organizationID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Template, 0)
	for rows.Next() {
		var item Template
		if err = rows.Scan(&item.ID, &item.Name, &item.Version, &item.Status, &item.TaskCount); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (input CreateTemplateInput) Validate() error {
	if n := len(strings.TrimSpace(input.Name)); n < 1 || n > 160 {
		return fmt.Errorf("name must contain between 1 and 160 characters")
	}
	if len(input.Tasks) > 100 {
		return fmt.Errorf("tasks cannot contain more than 100 items")
	}
	seen := map[int]bool{}
	for _, task := range input.Tasks {
		if n := len(strings.TrimSpace(task.Title)); n < 1 || n > 200 {
			return fmt.Errorf("task title must contain between 1 and 200 characters")
		}
		if task.DueOffsetDays < -90 || task.DueOffsetDays > 365 {
			return fmt.Errorf("task dueOffsetDays must be between -90 and 365")
		}
		if task.SortOrder < 0 || seen[task.SortOrder] {
			return fmt.Errorf("task sortOrder must be unique and non-negative")
		}
		seen[task.SortOrder] = true
		if !oneOf(task.Category, "it_setup", "hr", "facilities", "training", "documents", "other") {
			return fmt.Errorf("task category is invalid")
		}
		if !oneOf(task.AssigneeType, "employee", "manager", "hr_team", "it_team", "facilities_team") {
			return fmt.Errorf("task assigneeType is invalid")
		}
	}
	return nil
}
func (service *Service) CreateTemplate(ctx context.Context, org, actor string, input CreateTemplateInput) (Template, error) {
	if err := input.Validate(); err != nil {
		return Template{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result := Template{Name: strings.TrimSpace(input.Name), Version: 1, Status: "draft", TaskCount: len(input.Tasks)}
	err = tx.QueryRow(ctx, `INSERT INTO onboarding_templates (organization_id,name,created_by_user_id) VALUES ($1,$2,$3) RETURNING id::text`, org, result.Name, actor).Scan(&result.ID)
	if err != nil {
		return Template{}, err
	}
	for _, task := range input.Tasks {
		if _, err = tx.Exec(ctx, `INSERT INTO onboarding_template_tasks (template_id,title,description,category,assignee_type,due_offset_days,is_required,sort_order) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, result.ID, strings.TrimSpace(task.Title), nullIfBlank(task.Description), task.Category, task.AssigneeType, task.DueOffsetDays, task.IsRequired, task.SortOrder); err != nil {
			return Template{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'onboarding_template.created','onboarding_template',$3,'unknown',jsonb_build_object('taskCount',$4::int))`, org, actor, result.ID, result.TaskCount); err != nil {
		return Template{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return result, nil
}
func oneOf(value string, values ...string) bool {
	for _, item := range values {
		if value == item {
			return true
		}
	}
	return false
}
func nullIfBlank(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
