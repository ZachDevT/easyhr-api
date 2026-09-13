package operations

import (
	"context"
)

type FeatureFlag struct {
	ID             string  `json:"id"`
	Key            string  `json:"key"`
	Description    *string `json:"description"`
	DefaultEnabled bool    `json:"defaultEnabled"`
}

type FeatureFlagResult struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

type SetFeatureOverrideInput struct {
	Enabled bool `json:"enabled"`
}

func (s *Service) ListTenantFeatures(ctx context.Context, orgID string) ([]FeatureFlagResult, error) {
	// Returns a merged view of platform defaults + tenant overrides
	rows, err := s.pool.Query(ctx, `
		SELECT p.key, COALESCE(t.enabled, p.default_enabled) as enabled
		FROM platform_feature_flags p
		LEFT JOIN tenant_feature_flags t ON p.id = t.feature_flag_id AND t.organization_id = $1
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []FeatureFlagResult
	for rows.Next() {
		var f FeatureFlagResult
		if err := rows.Scan(&f.Key, &f.Enabled); err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	return items, nil
}

func (s *Service) ListPlatformFeatures(ctx context.Context) ([]FeatureFlag, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, key, description, default_enabled FROM platform_feature_flags ORDER BY key ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []FeatureFlag
	for rows.Next() {
		var f FeatureFlag
		if err := rows.Scan(&f.ID, &f.Key, &f.Description, &f.DefaultEnabled); err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	return items, nil
}

func (s *Service) SetTenantFeatureOverride(ctx context.Context, operatorID, key, orgID string, input SetFeatureOverrideInput) error {
	var flagID string
	err := s.pool.QueryRow(ctx, "SELECT id::text FROM platform_feature_flags WHERE key = $1", key).Scan(&flagID)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO tenant_feature_flags (organization_id, feature_flag_id, enabled, updated_by_operator_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (organization_id, feature_flag_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			updated_by_operator_id = EXCLUDED.updated_by_operator_id,
			updated_at = now()
	`, orgID, flagID, input.Enabled, operatorID)
	return err
}
