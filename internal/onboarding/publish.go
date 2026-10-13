package onboarding

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrTemplateNotFound = errors.New("onboarding template not found")
var ErrTemplateNotPublishable = errors.New("onboarding template is not publishable")

// PublishTemplate makes an immutable draft available for employee onboarding.
// A published template is never edited in place; changes are made as a new draft
// version, preserving the template version captured by existing instances.
func (service *Service) PublishTemplate(ctx context.Context, organizationID, actorUserID, templateID string) (Template, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result := Template{ID: templateID}
	err = tx.QueryRow(ctx, `
		SELECT name, version, status,
		       (SELECT count(*) FROM onboarding_template_tasks WHERE template_id = onboarding_templates.id)
		FROM onboarding_templates
		WHERE id = $1 AND organization_id = $2
		FOR UPDATE`, templateID, organizationID).
		Scan(&result.Name, &result.Version, &result.Status, &result.TaskCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateNotFound
	}
	if err != nil {
		return Template{}, err
	}
	if result.Status != "draft" || result.TaskCount == 0 {
		return Template{}, ErrTemplateNotPublishable
	}
	if _, err = tx.Exec(ctx, `UPDATE onboarding_templates SET status = 'published' WHERE id = $1`, templateID); err != nil {
		return Template{}, err
	}
	result.Status = "published"
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata)
		VALUES ($1, $2, 'onboarding_template.published', 'onboarding_template', $3, 'unknown', jsonb_build_object('version', $4::int))`,
		organizationID, actorUserID, templateID, result.Version); err != nil {
		return Template{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Template{}, err
	}
	return result, nil
}
