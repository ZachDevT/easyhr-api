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
		// Fallback: Check if user exists by external_identity_id and has an employee record in this workspace
		var uID, orgID string
		_ = resolver.pool.QueryRow(ctx, `
			SELECT u.id::text, w.organization_id::text
			FROM users u
			CROSS JOIN workspaces w
			WHERE u.external_identity_id = $1 AND w.subdomain = $2
			LIMIT 1`, externalID, workspace).Scan(&uID, &orgID)

		if uID != "" && orgID != "" {
			var empID string
			_ = resolver.pool.QueryRow(ctx, `
				SELECT id::text FROM employees
				WHERE organization_id = $1::uuid
				  AND (user_id = $2::uuid OR LOWER(work_email) = (SELECT LOWER(email) FROM users WHERE id = $2::uuid))
				LIMIT 1`, orgID, uID).Scan(&empID)

			if empID != "" {
				_, _ = resolver.pool.Exec(ctx, `UPDATE employees SET user_id = $1::uuid WHERE id = $2::uuid AND user_id IS NULL`, uID, empID)

				var memID string
				_ = resolver.pool.QueryRow(ctx, `
					INSERT INTO memberships (organization_id, user_id, status)
					VALUES ($1::uuid, $2::uuid, 'active')
					ON CONFLICT (organization_id, user_id) DO UPDATE SET status = 'active'
					RETURNING id::text`, orgID, uID).Scan(&memID)

				if memID != "" {
					var roleID string
					_ = resolver.pool.QueryRow(ctx, `SELECT id::text FROM roles WHERE organization_id = $1::uuid AND key = 'employee'`, orgID).Scan(&roleID)
					if roleID != "" {
						_, _ = resolver.pool.Exec(ctx, `INSERT INTO membership_roles (membership_id, role_id) VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`, memID, roleID)
					}

					principal.UserID = uID
					principal.MembershipID = memID
					principal.OrganizationID = orgID
					err = nil
				}
			}
		}

		if err != nil {
			return Principal{}, errMembershipNotFound
		}
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
