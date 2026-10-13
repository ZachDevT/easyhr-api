package tenancy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SetupResult struct {
	OrganizationID string `json:"organizationId"`
	WorkspaceID    string `json:"workspaceId"`
	MembershipID   string `json:"membershipId"`
	Subdomain      string `json:"subdomain"`
	WorkspaceURL   string `json:"workspaceUrl"`
}

var (
	ErrSetupUserNotProvisioned = errors.New("authenticated user has not been provisioned locally")
	ErrSetupConflict           = errors.New("organization setup conflicts with an existing record")
	ErrIdempotencyKeyReused    = errors.New("idempotency key was reused with a different request")
)

type SetupService struct {
	pool *pgxpool.Pool
}

func NewSetupService(pool *pgxpool.Pool) *SetupService {
	return &SetupService{pool: pool}
}

func (service *SetupService) CreateOrganization(ctx context.Context, externalIdentityID, idempotencyKey string, input OrganizationSetupInput) (SetupResult, error) {
	if err := input.Validate(); err != nil {
		return SetupResult{}, err
	}
	if len(idempotencyKey) < 16 || len(idempotencyKey) > 255 {
		return SetupResult{}, fmt.Errorf("Idempotency-Key must contain between 16 and 255 characters")
	}
	requestHash, err := organizationSetupHash(input)
	if err != nil {
		return SetupResult{}, fmt.Errorf("hash organization setup: %w", err)
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SetupResult{}, fmt.Errorf("begin organization setup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if storedResult, found, err := findStoredSetupResult(ctx, tx, externalIdentityID, idempotencyKey, requestHash); err != nil {
		return SetupResult{}, err
	} else if found {
		return storedResult, nil
	}

	var userID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM users WHERE external_identity_id = $1`, externalIdentityID).Scan(&userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			userEmail := input.ContactEmail
			if userEmail == "" {
				userEmail = fmt.Sprintf("owner_%s@easyhr.app", externalIdentityID)
			}
			err = tx.QueryRow(ctx, `
				INSERT INTO users (external_identity_id, email, first_name, last_name)
				VALUES ($1, $2, 'Company', 'Owner')
				ON CONFLICT (external_identity_id) DO UPDATE SET updated_at = now()
				RETURNING id::text`, externalIdentityID, userEmail).Scan(&userID)
			if err != nil {
				return SetupResult{}, fmt.Errorf("auto-provision setup user: %w", err)
			}
		} else {
			return SetupResult{}, fmt.Errorf("find setup user: %w", err)
		}
	}

	result := SetupResult{
		Subdomain:    input.Subdomain,
		WorkspaceURL: fmt.Sprintf("https://%s.easyhr.pro", input.Subdomain),
	}
	if err := tx.QueryRow(ctx, `
        INSERT INTO organizations (name, country_code, timezone)
        VALUES ($1, $2, $3)
        RETURNING id::text`, input.DisplayName, input.CountryCode, input.Timezone).Scan(&result.OrganizationID); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO organization_legal_profiles (
            organization_id, legal_name, registration_number, tax_identifier, country_code,
            industry, company_size_band, website_url, contact_email, contact_phone,
            address_line_1, address_line_2, city, administrative_area, postal_code
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		result.OrganizationID, input.LegalName, nullIfBlank(input.RegistrationNumber), nullIfBlank(input.TaxIdentifier), input.CountryCode,
		nullIfBlank(input.Industry), nullIfBlank(input.CompanySizeBand), nullIfBlank(input.WebsiteURL), nullIfBlank(input.ContactEmail), nullIfBlank(input.ContactPhone),
		nullIfBlank(input.Address.Line1), nullIfBlank(input.Address.Line2), nullIfBlank(input.Address.City), nullIfBlank(input.Address.AdministrativeArea), nullIfBlank(input.Address.PostalCode),
	); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}
	if err := tx.QueryRow(ctx, `
        INSERT INTO workspaces (organization_id, subdomain, brand_color)
        VALUES ($1, $2, $3)
        RETURNING id::text`, result.OrganizationID, input.Subdomain, input.BrandColor).Scan(&result.WorkspaceID); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}
	if err := tx.QueryRow(ctx, `
        INSERT INTO memberships (organization_id, user_id, status)
        VALUES ($1, $2, 'active')
        RETURNING id::text`, result.OrganizationID, userID).Scan(&result.MembershipID); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}

	var ownerRoleID string
	if err := tx.QueryRow(ctx, `
        INSERT INTO roles (organization_id, key, display_name, is_system)
        VALUES ($1, 'company_owner', 'Company owner', true)
        RETURNING id::text`, result.OrganizationID).Scan(&ownerRoleID); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO role_permissions (role_id, permission_key)
        SELECT $1, key FROM permissions
        ON CONFLICT DO NOTHING`, ownerRoleID); err != nil {
		return SetupResult{}, fmt.Errorf("grant owner permissions: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO membership_roles (membership_id, role_id) VALUES ($1, $2)`, result.MembershipID, ownerRoleID); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}

	// Seed default workspace roles (employee, hr_manager)
	var employeeRoleID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO roles (organization_id, key, display_name, is_system)
		VALUES ($1, 'employee', 'Employee', true)
		RETURNING id::text`, result.OrganizationID).Scan(&employeeRoleID); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key)
		VALUES ($1, 'organization:read'), ($1, 'employees:read')
		ON CONFLICT DO NOTHING`, employeeRoleID); err != nil {
		return SetupResult{}, fmt.Errorf("grant employee permissions: %w", err)
	}

	var hrRoleID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO roles (organization_id, key, display_name, is_system)
		VALUES ($1, 'hr_manager', 'HR Manager', true)
		RETURNING id::text`, result.OrganizationID).Scan(&hrRoleID); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}
	for _, department := range input.InitialDepartments {
		deptName := strings.TrimSpace(department)
		deptCode := strings.ToUpper(strings.ReplaceAll(deptName, " ", "_"))
		if len(deptCode) > 20 {
			deptCode = deptCode[:20]
		}
		if _, err := tx.Exec(ctx, `INSERT INTO departments (organization_id, name, code) VALUES ($1, $2, $3)`, result.OrganizationID, deptName, deptCode); err != nil {
			return SetupResult{}, normalizeSetupError(err)
		}
	}
	if input.Headquarters.Name != "" {
		if err := insertHeadquarters(ctx, tx, result.OrganizationID, input.Headquarters); err != nil {
			return SetupResult{}, normalizeSetupError(err)
		}
	}
	for platform, link := range input.SocialLinks {
		if _, err := tx.Exec(ctx, `INSERT INTO organization_social_links (organization_id, platform, url) VALUES ($1, $2, $3)`, result.OrganizationID, platform, link); err != nil {
			return SetupResult{}, normalizeSetupError(err)
		}
	}
	reqID := requestIDFromContext(ctx)
	if reqID == "" {
		reqID = "unknown"
	}
	auditMetadata, _ := json.Marshal(map[string]any{"subdomain": result.Subdomain})
	if _, err := tx.Exec(ctx, `
        INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata)
        VALUES ($1, $2, 'organization.created', 'organization', $1, $3, $4::jsonb)`,
		result.OrganizationID, userID, reqID, auditMetadata); err != nil {
		return SetupResult{}, fmt.Errorf("write setup audit event: %w", err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return SetupResult{}, fmt.Errorf("marshal setup idempotency response: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO bootstrap_idempotency_keys (external_identity_id, route, key, request_hash, response_body, expires_at)
        VALUES ($1, '/api/v1/setup/organizations', $2, $3, $4::jsonb, now() + interval '24 hours')`,
		externalIdentityID, idempotencyKey, requestHash, resultJSON); err != nil {
		return SetupResult{}, normalizeSetupError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SetupResult{}, fmt.Errorf("commit organization setup: %w", err)
	}
	return result, nil
}

func findStoredSetupResult(ctx context.Context, tx pgx.Tx, externalIdentityID, idempotencyKey, requestHash string) (SetupResult, bool, error) {
	var storedHash string
	var responseJSON []byte
	err := tx.QueryRow(ctx, `
        SELECT request_hash, response_body
        FROM bootstrap_idempotency_keys
        WHERE external_identity_id = $1 AND route = '/api/v1/setup/organizations' AND key = $2 AND expires_at > now()`,
		externalIdentityID, idempotencyKey).Scan(&storedHash, &responseJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return SetupResult{}, false, nil
	}
	if err != nil {
		return SetupResult{}, false, fmt.Errorf("read setup idempotency record: %w", err)
	}
	if storedHash != requestHash {
		return SetupResult{}, false, ErrIdempotencyKeyReused
	}
	var result SetupResult
	if err := json.Unmarshal(responseJSON, &result); err != nil {
		return SetupResult{}, false, fmt.Errorf("decode setup idempotency response: %w", err)
	}
	return result, true, nil
}

func organizationSetupHash(input OrganizationSetupInput) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func insertHeadquarters(ctx context.Context, tx pgx.Tx, organizationID string, location LocationInput) error {
	var locationID string
	err := tx.QueryRow(ctx, `
        INSERT INTO locations (organization_id, name, country_code, timezone, address_line_1, address_line_2, city, administrative_area, postal_code)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
        RETURNING id::text`, organizationID, location.Name, location.CountryCode, location.Timezone,
		nullIfBlank(location.Address.Line1), nullIfBlank(location.Address.Line2), nullIfBlank(location.Address.City), nullIfBlank(location.Address.AdministrativeArea), nullIfBlank(location.Address.PostalCode),
	).Scan(&locationID)
	if err != nil || location.Geofence == nil {
		return err
	}
	_, err = tx.Exec(ctx, `
        INSERT INTO geofence_rules (location_id, enabled, latitude, longitude, radius_meters, accuracy_tolerance_meters)
        VALUES ($1, true, $2, $3, $4, $5)`, locationID, location.Geofence.Latitude, location.Geofence.Longitude, location.Geofence.RadiusMeters, location.Geofence.AccuracyToleranceMeters)
	return err
}

func nullIfBlank(value string) any {
	if value = strings.TrimSpace(value); value == "" {
		return nil
	}
	return value
}

func normalizeSetupError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		slog.Error("setup unique constraint violation",
			"table", pgErr.TableName,
			"constraint", pgErr.ConstraintName,
			"detail", pgErr.Detail,
			"message", pgErr.Message,
		)
		return fmt.Errorf("%w: %s (%s)", ErrSetupConflict, pgErr.ConstraintName, pgErr.Detail)
	}
	return err
}
