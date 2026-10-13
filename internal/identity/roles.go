package identity

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

type Role struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description *string  `json:"description,omitempty"`
	IsSystem    bool     `json:"isSystem"`
	IsCustom    bool     `json:"isCustom"`
	Permissions []string `json:"permissions"`
	MemberCount int      `json:"memberCount"`
}

type CreateRoleInput struct {
	Key         string   `json:"key,omitempty"`
	Name        string   `json:"name"`
	Description *string  `json:"description,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

type UpdateRolePermissionsInput struct {
	PermissionNames []string `json:"permissionNames"`
}

type PermissionItem struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

var keyRegex = regexp.MustCompile(`[^a-z0-9_]`)

func (s *Resolver) ListRoles(ctx context.Context, orgID string) ([]Role, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 
			r.id::text,
			r.key,
			r.display_name,
			r.is_system,
			COUNT(DISTINCT mr.membership_id) AS member_count,
			COALESCE(ARRAY_AGG(DISTINCT rp.permission_key) FILTER (WHERE rp.permission_key IS NOT NULL), '{}') AS permissions
		FROM roles r
		LEFT JOIN membership_roles mr ON mr.role_id = r.id
		LEFT JOIN role_permissions rp ON rp.role_id = r.id
		WHERE r.organization_id = $1::uuid OR r.organization_id IS NULL
		GROUP BY r.id, r.key, r.display_name, r.is_system, r.created_at
		ORDER BY r.is_system DESC, r.created_at ASC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Role, 0)
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.ID, &r.Key, &r.Name, &r.IsSystem, &r.MemberCount, &r.Permissions); err != nil {
			return nil, err
		}
		r.IsCustom = !r.IsSystem
		items = append(items, r)
	}
	return items, nil
}

func (s *Resolver) ListAllPermissions(ctx context.Context) ([]PermissionItem, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, description FROM permissions ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PermissionItem
	for rows.Next() {
		var p PermissionItem
		if err := rows.Scan(&p.Key, &p.Description); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}

func (s *Resolver) CreateRole(ctx context.Context, orgID string, input CreateRoleInput) (Role, error) {
	key := strings.ToLower(strings.TrimSpace(input.Key))
	if key == "" {
		key = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(input.Name), " ", "_"))
	}
	key = keyRegex.ReplaceAllString(key, "")
	if len(key) < 2 {
		key = "role_" + key
	}
	if len(key) > 60 {
		key = key[:60]
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Role{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var r Role
	err = tx.QueryRow(ctx, `
		INSERT INTO roles (organization_id, key, display_name, is_system)
		VALUES ($1, $2, $3, false)
		RETURNING id::text, key, display_name, is_system
	`, orgID, key, input.Name).Scan(&r.ID, &r.Key, &r.Name, &r.IsSystem)
	if err != nil {
		return Role{}, err
	}
	r.IsCustom = !r.IsSystem
	r.Permissions = make([]string, 0)

	for _, perm := range input.Permissions {
		perm = strings.TrimSpace(perm)
		if perm == "" {
			continue
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_key)
			SELECT $1, key FROM permissions WHERE key = $2
			ON CONFLICT DO NOTHING
		`, r.ID, perm)
		if err == nil {
			r.Permissions = append(r.Permissions, perm)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return Role{}, err
	}
	return r, nil
}

func (s *Resolver) UpdateRolePermissions(ctx context.Context, orgID, roleID string, input UpdateRolePermissionsInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify role belongs to organization and is custom
	var isSystem bool
	err = tx.QueryRow(ctx, "SELECT is_system FROM roles WHERE id = $1 AND (organization_id = $2 OR organization_id IS NULL)", roleID, orgID).Scan(&isSystem)
	if err != nil {
		return fmt.Errorf("role not found: %w", err)
	}
	if isSystem {
		return fmt.Errorf("system roles cannot be modified")
	}

	// Delete old permissions
	_, err = tx.Exec(ctx, "DELETE FROM role_permissions WHERE role_id = $1", roleID)
	if err != nil {
		return err
	}

	// Insert new permissions
	for _, pKey := range input.PermissionNames {
		pKey = strings.TrimSpace(pKey)
		if pKey == "" {
			continue
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_key)
			SELECT $1, key FROM permissions WHERE key = $2
			ON CONFLICT DO NOTHING
		`, roleID, pKey)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Resolver) AssignRoleToEmployee(ctx context.Context, orgID, employeeID, roleID string) error {
	// Find membership_id for this employee
	var memID string
	err := s.pool.QueryRow(ctx, `
		SELECT m.id::text 
		FROM memberships m
		JOIN employees e ON e.user_id = m.user_id AND e.organization_id = m.organization_id
		WHERE e.id = $1 AND e.organization_id = $2
	`, employeeID, orgID).Scan(&memID)
	if err != nil {
		return fmt.Errorf("employee membership not found: %w", err)
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
