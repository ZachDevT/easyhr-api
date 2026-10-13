package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/user"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrIdentityProfileIncomplete = errors.New("identity profile is incomplete or email is unverified")
	ErrIdentityEmailConflict     = errors.New("identity email is already linked to another local user")
)

type Provisioner struct {
	pool   *pgxpool.Pool
	client *user.Client
}

func NewProvisioner(pool *pgxpool.Pool, clerkSecretKey string) *Provisioner {
	secretKey := clerkSecretKey
	return &Provisioner{pool: pool, client: user.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secretKey}})}
}

type ProvisionInput struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Email     string `json:"email"`
}

func (provisioner *Provisioner) Provision(ctx context.Context, externalIdentityID string) error {
	return provisioner.ProvisionWithInput(ctx, externalIdentityID, ProvisionInput{})
}

func (provisioner *Provisioner) ProvisionWithInput(ctx context.Context, externalIdentityID string, fallback ProvisionInput) error {
	var firstName, lastName, email string

	externalUser, err := provisioner.client.Get(ctx, externalIdentityID)
	if err == nil && externalUser != nil {
		firstName, lastName, email = extractUserProfile(externalUser)
	}

	if fallback.FirstName = strings.TrimSpace(fallback.FirstName); fallback.FirstName != "" {
		firstName = fallback.FirstName
	}
	if fallback.LastName = strings.TrimSpace(fallback.LastName); fallback.LastName != "" {
		lastName = fallback.LastName
	}
	if fallback.Email = strings.TrimSpace(fallback.Email); fallback.Email != "" {
		email = fallback.Email
	}

	if firstName == "" {
		firstName = "User"
	}
	if lastName == "" {
		lastName = "Admin"
	}
	if email == "" {
		email = fmt.Sprintf("user_%s@easyhr.app", externalIdentityID)
	}

	// 1. First check if a user with LOWER(email) already exists (e.g. pre-created applicant or invited employee)
	var existingUserID string
	err = provisioner.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE LOWER(email) = LOWER($1)`, email).Scan(&existingUserID)
	if err == nil && existingUserID != "" {
		// Update existing user with the authenticated Clerk external_identity_id
		_, err = provisioner.pool.Exec(ctx, `
			UPDATE users
			SET external_identity_id = $1,
			    first_name = COALESCE(NULLIF($2, ''), first_name),
			    last_name = COALESCE(NULLIF($3, ''), last_name),
			    updated_at = now()
			WHERE id = $4::uuid`,
			externalIdentityID, firstName, lastName, existingUserID)
		if err != nil {
			return fmt.Errorf("update user external identity: %w", err)
		}
	} else {
		// Insert new user or update on conflict of external_identity_id
		err = provisioner.pool.QueryRow(ctx, `
			INSERT INTO users (external_identity_id, email, first_name, last_name)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (external_identity_id) DO UPDATE
			SET email = EXCLUDED.email, first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name, updated_at = now()
			RETURNING id::text`,
			externalIdentityID, email, firstName, lastName).Scan(&existingUserID)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return ErrIdentityEmailConflict
			}
			return fmt.Errorf("upsert local user: %w", err)
		}
	}

	// 2. Auto-link to any existing employee records matching this email or user_id
	if existingUserID != "" {
		rows, errEmp := provisioner.pool.Query(ctx, `
			SELECT id::text, organization_id::text
			FROM employees
			WHERE LOWER(work_email) = LOWER($1) OR user_id = $2::uuid`,
			email, existingUserID)
		if errEmp == nil {
			defer rows.Close()
			for rows.Next() {
				var empID, orgID string
				if err := rows.Scan(&empID, &orgID); err == nil {
					// Link employee.user_id
					_, _ = provisioner.pool.Exec(ctx, `
						UPDATE employees SET user_id = $1::uuid WHERE id = $2::uuid AND user_id IS NULL`,
						existingUserID, empID)

					// Ensure active membership
					var membershipID string
					_ = provisioner.pool.QueryRow(ctx, `
						INSERT INTO memberships (organization_id, user_id, status)
						VALUES ($1::uuid, $2::uuid, 'active')
						ON CONFLICT (organization_id, user_id) DO UPDATE SET status = 'active'
						RETURNING id::text`,
						orgID, existingUserID).Scan(&membershipID)

					if membershipID != "" {
						// Ensure employee role
						var roleID string
						_ = provisioner.pool.QueryRow(ctx, `
							SELECT id::text FROM roles WHERE organization_id = $1::uuid AND key = 'employee'`,
							orgID).Scan(&roleID)
						if roleID != "" {
							_, _ = provisioner.pool.Exec(ctx, `
								INSERT INTO membership_roles (membership_id, role_id)
								VALUES ($1::uuid, $2::uuid)
								ON CONFLICT DO NOTHING`,
								membershipID, roleID)
						}
					}
				}
			}
		}
	}

	return nil
}

func extractUserProfile(externalUser *clerk.User) (firstName, lastName, email string) {
	if externalUser == nil {
		return "", "", ""
	}
	if externalUser.FirstName != nil {
		firstName = strings.TrimSpace(*externalUser.FirstName)
	}
	if externalUser.LastName != nil {
		lastName = strings.TrimSpace(*externalUser.LastName)
	}
	if firstName == "" && externalUser.Username != nil {
		firstName = strings.TrimSpace(*externalUser.Username)
	}

	for _, address := range externalUser.EmailAddresses {
		if address != nil && externalUser.PrimaryEmailAddressID != nil && address.ID == *externalUser.PrimaryEmailAddressID {
			email = strings.TrimSpace(address.EmailAddress)
			break
		}
	}
	if email == "" && len(externalUser.EmailAddresses) > 0 {
		for _, address := range externalUser.EmailAddresses {
			if address != nil && strings.TrimSpace(address.EmailAddress) != "" {
				email = strings.TrimSpace(address.EmailAddress)
				break
			}
		}
	}
	return firstName, lastName, email
}
