package identity

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const resolvePrincipalSQL = `
SELECT
    u.id::text,
    m.id::text,
    m.organization_id::text
FROM users u
JOIN memberships m ON m.user_id = u.id AND m.status = 'active'
JOIN workspaces w ON w.organization_id = m.organization_id
WHERE u.external_identity_id = $1
  AND w.subdomain = $2`

const resolvePermissionsSQL = `
SELECT DISTINCT rp.permission_key
FROM membership_roles mr
JOIN role_permissions rp ON rp.role_id = mr.role_id
WHERE mr.membership_id = $1`

func (resolver *Resolver) resolve(ctx context.Context, externalID, workspace string) (Principal, error) {
	principal := Principal{ExternalID: externalID, Permissions: make(map[string]struct{})}
	err := resolver.pool.QueryRow(ctx, resolvePrincipalSQL, externalID, workspace).Scan(
		&principal.UserID,
		&principal.MembershipID,
		&principal.OrganizationID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Principal{}, errMembershipNotFound
	}
	if err != nil {
		return Principal{}, err
	}

	rows, err := resolver.pool.Query(ctx, resolvePermissionsSQL, principal.MembershipID)
	if err != nil {
		return Principal{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return Principal{}, err
		}
		principal.Permissions[permission] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return Principal{}, err
	}
	return principal, nil
}
