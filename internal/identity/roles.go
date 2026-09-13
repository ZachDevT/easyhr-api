package identity

import (
	"context"
)

type Role struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	IsCustom    bool    `json:"isCustom"`
}

type CreateRoleInput struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

type UpdateRolePermissionsInput struct {
	PermissionNames []string `json:"permissionNames"`
}

func (s *Resolver) ListRoles(ctx context.Context, orgID string) ([]Role, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, name, description, is_custom FROM roles WHERE organization_id = $1 OR is_custom = false ORDER BY name", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Role
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Name, &r.Description, &r.IsCustom); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, nil
}

func (s *Resolver) CreateRole(ctx context.Context, orgID string, input CreateRoleInput) (Role, error) {
	var r Role
	err := s.pool.QueryRow(ctx, `
		INSERT INTO roles (organization_id, name, description, is_custom)
		VALUES ($1, $2, $3, true)
		RETURNING id::text, name, description, is_custom
	`, orgID, input.Name, input.Description).Scan(
		&r.ID, &r.Name, &r.Description, &r.IsCustom,
	)
	return r, err
}

func (s *Resolver) UpdateRolePermissions(ctx context.Context, orgID, roleID string, input UpdateRolePermissionsInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Ensure role belongs to org and is custom
	var exists bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM roles WHERE id = $1 AND organization_id = $2 AND is_custom = true)", roleID, orgID).Scan(&exists)
	if err != nil || !exists {
		return err // Or return forbidden
	}

	// Delete old permissions
	_, err = tx.Exec(ctx, "DELETE FROM role_permissions WHERE role_id = $1", roleID)
	if err != nil {
		return err
	}

	// Insert new permissions
	for _, pName := range input.PermissionNames {
		_, err = tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_id)
			SELECT $1, id FROM permissions WHERE name = $2
		`, roleID, pName)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Resolver) AssignRoleToEmployee(ctx context.Context, orgID, employeeID, roleID string) error {
	// Need to find the membership_id for this employee
	var memID string
	err := s.pool.QueryRow(ctx, `
		SELECT m.id::text 
		FROM memberships m
		JOIN employees e ON e.user_id = m.user_id AND e.organization_id = m.organization_id
		WHERE e.id = $1 AND e.organization_id = $2
	`, employeeID, orgID).Scan(&memID)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO membership_roles (membership_id, role_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, memID, roleID)
	return err
}

type AssignRole struct {
	RoleID string `json:"roleId"`
}
