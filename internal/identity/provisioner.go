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

	_, err = provisioner.pool.Exec(ctx, `
        INSERT INTO users (external_identity_id, email, first_name, last_name)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (external_identity_id) DO UPDATE
        SET email = EXCLUDED.email, first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name, updated_at = now()`,
		externalIdentityID, email, firstName, lastName)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrIdentityEmailConflict
		}
		return fmt.Errorf("upsert local user: %w", err)
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
