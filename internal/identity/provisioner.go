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

func (provisioner *Provisioner) Provision(ctx context.Context, externalIdentityID string) error {
	externalUser, err := provisioner.client.Get(ctx, externalIdentityID)
	if err != nil {
		return fmt.Errorf("load identity-provider user: %w", err)
	}
	firstName, lastName, email, err := verifiedUserProfile(externalUser)
	if err != nil {
		return err
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

func verifiedUserProfile(externalUser *clerk.User) (firstName, lastName, email string, err error) {
	if externalUser == nil || externalUser.FirstName == nil || externalUser.LastName == nil {
		return "", "", "", ErrIdentityProfileIncomplete
	}
	firstName, lastName = strings.TrimSpace(*externalUser.FirstName), strings.TrimSpace(*externalUser.LastName)
	if firstName == "" || lastName == "" || externalUser.PrimaryEmailAddressID == nil {
		return "", "", "", ErrIdentityProfileIncomplete
	}
	for _, address := range externalUser.EmailAddresses {
		if address != nil && address.ID == *externalUser.PrimaryEmailAddressID && address.Verification != nil && address.Verification.Status == "verified" {
			if email = strings.TrimSpace(address.EmailAddress); email != "" {
				return firstName, lastName, email, nil
			}
		}
	}
	return "", "", "", ErrIdentityProfileIncomplete
}
