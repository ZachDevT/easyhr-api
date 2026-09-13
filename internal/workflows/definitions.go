package workflows

import (
	"context"
)

type WorkflowDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TriggerType string `json:"triggerType"`
	Status      string `json:"status"`
}

type CreateDefinitionInput struct {
	Name        string `json:"name"`
	TriggerType string `json:"triggerType"`
}

func (service *Service) ListDefinitions(ctx context.Context, organizationID string) ([]WorkflowDefinition, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, name, trigger_type, status 
		FROM workflow_definitions 
		WHERE organization_id = $1 
		ORDER BY created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []WorkflowDefinition{}
	for rows.Next() {
		var item WorkflowDefinition
		if err := rows.Scan(&item.ID, &item.Name, &item.TriggerType, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) CreateDefinition(ctx context.Context, organizationID, actorUserID string, input CreateDefinitionInput) (WorkflowDefinition, error) {
	var item WorkflowDefinition
	err := service.pool.QueryRow(ctx, `
		INSERT INTO workflow_definitions (organization_id, name, trigger_type, status, created_by_user_id)
		VALUES ($1, $2, $3, 'active', $4)
		RETURNING id::text, name, trigger_type, status
	`, organizationID, input.Name, input.TriggerType, actorUserID).Scan(
		&item.ID, &item.Name, &item.TriggerType, &item.Status,
	)
	if err != nil {
		return WorkflowDefinition{}, err
	}
	return item, nil
}
