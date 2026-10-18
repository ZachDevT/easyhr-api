package httpapi

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/easyhr/easyhr-be/internal/identity"
)

type OrganizationDetailsUpdateInput struct {
	DisplayName        string `json:"displayName"`
	LegalName          string `json:"legalName"`
	RegistrationNumber string `json:"registrationNumber"`
	TaxIdentifier      string `json:"taxIdentifier"`
	WebsiteURL         string `json:"websiteUrl"`
	ContactEmail       string `json:"contactEmail"`
	ContactPhone       string `json:"contactPhone"`
	Industry           string `json:"industry"`
	AddressLine1       string `json:"addressLine1"`
	City               string `json:"city"`
	BrandColor         string `json:"brandColor"`
}

type UserProfileUpdateInput struct {
	FirstName string `json:"firstName"`
	LastName  string `json:"lastName"`
	Phone     string `json:"phone"`
}

type CreateLocationRequest struct {
	Name         string `json:"name"`
	CountryCode  string `json:"countryCode"`
	Timezone     string `json:"timezone"`
	AddressLine1 string `json:"addressLine1"`
	City         string `json:"city"`
}

func (s *Server) getMe(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	permissions := make([]string, 0, len(principal.Permissions))
	for permission := range principal.Permissions {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)

	ctx := c.Request().Context()

	// 1. User info
	var userEmail, userFirstName, userLastName, userAvatarURL, userPhone string
	_ = s.pool.QueryRow(ctx, `
		SELECT email, first_name, last_name, COALESCE(avatar_url, ''), COALESCE(phone, '')
		FROM users WHERE id = $1`, principal.UserID).Scan(&userEmail, &userFirstName, &userLastName, &userAvatarURL, &userPhone)

	var userRole string
	_ = s.pool.QueryRow(ctx, `
		SELECT COALESCE(r.display_name, 'Company Owner')
		FROM membership_roles mr
		JOIN roles r ON r.id = mr.role_id
		WHERE mr.membership_id = $1
		LIMIT 1`, principal.MembershipID).Scan(&userRole)
	if userRole == "" {
		userRole = "Company Owner"
	}

	var empStatus, empID string
	_ = s.pool.QueryRow(ctx, `
		SELECT id::text, status
		FROM employees
		WHERE organization_id = $1
		  AND (user_id = $2 OR (user_id IS NULL AND LOWER(work_email) = LOWER($3)))
		ORDER BY created_at DESC
		LIMIT 1`, principal.OrganizationID, principal.UserID, userEmail).Scan(&empID, &empStatus)
	if empID == "" {
		_ = s.people.EnsureMemberEmployees(ctx, principal.OrganizationID)
		_ = s.pool.QueryRow(ctx, `
			SELECT id::text, status
			FROM employees
			WHERE organization_id = $1
			  AND (user_id = $2 OR (user_id IS NULL AND LOWER(work_email) = LOWER($3)))
			ORDER BY created_at DESC
			LIMIT 1`, principal.OrganizationID, principal.UserID, userEmail).Scan(&empID, &empStatus)
	}
	if empID != "" && principal.UserID != "" {
		_, _ = s.pool.Exec(ctx, "UPDATE employees SET user_id = $1 WHERE id = $2::uuid AND user_id IS NULL", principal.UserID, empID)
	}

	// 2. Organization info
	var orgName, orgCountryCode, orgTimezone string
	_ = s.pool.QueryRow(ctx, `
		SELECT name, country_code, timezone
		FROM organizations WHERE id = $1`, principal.OrganizationID).Scan(&orgName, &orgCountryCode, &orgTimezone)

	// 3. Workspace info
	var workspaceID, subdomain, brandColor, logoURL string
	_ = s.pool.QueryRow(ctx, `
		SELECT id::text, subdomain, brand_color, COALESCE(logo_url, '')
		FROM workspaces WHERE organization_id = $1`, principal.OrganizationID).Scan(&workspaceID, &subdomain, &brandColor, &logoURL)

	// 4. Legal Profile info
	var legalName, regNumber, taxID, lpCountry, lpIndustry, lpSizeBand, websiteURL, contactEmail, contactPhone, addr1, addr2, city, adminArea, postalCode string
	_ = s.pool.QueryRow(ctx, `
		SELECT COALESCE(legal_name, ''), COALESCE(registration_number, ''), COALESCE(tax_identifier, ''),
		       COALESCE(country_code, ''), COALESCE(industry, ''), COALESCE(company_size_band, ''),
		       COALESCE(website_url, ''), COALESCE(contact_email, ''), COALESCE(contact_phone, ''),
		       COALESCE(address_line_1, ''), COALESCE(address_line_2, ''), COALESCE(city, ''),
		       COALESCE(administrative_area, ''), COALESCE(postal_code, '')
		FROM organization_legal_profiles WHERE organization_id = $1`, principal.OrganizationID).Scan(
		&legalName, &regNumber, &taxID, &lpCountry, &lpIndustry, &lpSizeBand,
		&websiteURL, &contactEmail, &contactPhone, &addr1, &addr2, &city, &adminArea, &postalCode,
	)

	return c.JSON(http.StatusOK, map[string]any{
		"userId":         principal.UserID,
		"membershipId":   principal.MembershipID,
		"organizationId": principal.OrganizationID,
		"permissions":    permissions,
		"user": map[string]any{
			"id":             principal.UserID,
			"email":          userEmail,
			"firstName":      userFirstName,
			"lastName":       userLastName,
			"phone":          userPhone,
			"avatarUrl":      userAvatarURL,
			"role":           userRole,
			"employeeStatus": empStatus,
			"employeeId":     empID,
		},
		"organization": map[string]any{
			"id":          principal.OrganizationID,
			"name":        orgName,
			"countryCode": orgCountryCode,
			"timezone":    orgTimezone,
		},
		"workspace": map[string]any{
			"id":         workspaceID,
			"subdomain":  subdomain,
			"brandColor": brandColor,
			"logoUrl":    logoURL,
		},
		"legalProfile": map[string]any{
			"legalName":          legalName,
			"registrationNumber": regNumber,
			"taxIdentifier":      taxID,
			"countryCode":        lpCountry,
			"industry":           lpIndustry,
			"companySizeBand":    lpSizeBand,
			"websiteUrl":         websiteURL,
			"contactEmail":       contactEmail,
			"contactPhone":       contactPhone,
			"addressLine1":       addr1,
			"addressLine2":       addr2,
			"city":               city,
			"administrativeArea": adminArea,
			"postalCode":         postalCode,
		},
	})
}

func (s *Server) updateOrganizationDetails(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	var input OrganizationDetailsUpdateInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}

	ctx := c.Request().Context()

	if input.DisplayName != "" {
		_, _ = s.pool.Exec(ctx, `UPDATE organizations SET name = $1, updated_at = now() WHERE id = $2`, input.DisplayName, principal.OrganizationID)
	}

	if input.BrandColor != "" && strings.HasPrefix(input.BrandColor, "#") {
		_, _ = s.pool.Exec(ctx, `UPDATE workspaces SET brand_color = $1, updated_at = now() WHERE organization_id = $2`, input.BrandColor, principal.OrganizationID)
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO organization_legal_profiles (
			organization_id, legal_name, registration_number, tax_identifier,
			industry, website_url, contact_email, contact_phone, address_line_1, city, country_code
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'UG')
		ON CONFLICT (organization_id) DO UPDATE SET
			legal_name = COALESCE(NULLIF(EXCLUDED.legal_name, ''), organization_legal_profiles.legal_name),
			registration_number = EXCLUDED.registration_number,
			tax_identifier = EXCLUDED.tax_identifier,
			industry = EXCLUDED.industry,
			website_url = EXCLUDED.website_url,
			contact_email = EXCLUDED.contact_email,
			contact_phone = EXCLUDED.contact_phone,
			address_line_1 = EXCLUDED.address_line_1,
			city = EXCLUDED.city,
			updated_at = now()`,
		principal.OrganizationID, input.LegalName, input.RegistrationNumber, input.TaxIdentifier,
		input.Industry, input.WebsiteURL, input.ContactEmail, input.ContactPhone, input.AddressLine1, input.City,
	)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "organization.update_failed", "Update failed", err.Error())
	}

	return s.getMe(c)
}

func (s *Server) uploadBrandingLogo(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	if s.storage == nil {
		return problem(c, http.StatusInternalServerError, "storage.unavailable", "Storage unavailable", "Cloudflare R2 storage is not configured")
	}

	data, contentType, ext, err := extractImagePayload(c, "logo")
	if err != nil {
		return problem(c, http.StatusBadRequest, "upload.invalid", "Invalid upload", err.Error())
	}

	key := fmt.Sprintf("organizations/%s/branding/logo_%d.%s", principal.OrganizationID, time.Now().UnixNano(), ext)
	if err := s.storage.PutObject(c.Request().Context(), key, contentType, bytes.NewReader(data), int64(len(data))); err != nil {
		return problem(c, http.StatusInternalServerError, "storage.upload_failed", "Storage error", fmt.Sprintf("Failed to store in Cloudflare R2: %v", err))
	}

	publicURL := s.buildPublicAssetURL(c, key)

	_, err = s.pool.Exec(c.Request().Context(), `
		UPDATE workspaces
		SET logo_url = $1, updated_at = now()
		WHERE organization_id = $2`, publicURL, principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "database.update_failed", "Database error", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{
		"logoUrl": publicURL,
		"key":     key,
	})
}

func (s *Server) updateUserProfile(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	var input UserProfileUpdateInput
	if err := c.Bind(&input); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}

	_, err := s.pool.Exec(c.Request().Context(), `
		UPDATE users
		SET first_name = COALESCE(NULLIF($1, ''), first_name),
		    last_name = COALESCE(NULLIF($2, ''), last_name),
		    phone = $3,
		    updated_at = now()
		WHERE id = $4`, input.FirstName, input.LastName, input.Phone, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "profile.update_failed", "Profile update error", err.Error())
	}

	return s.getMe(c)
}

func (s *Server) uploadUserAvatar(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	if s.storage == nil {
		return problem(c, http.StatusInternalServerError, "storage.unavailable", "Storage unavailable", "Cloudflare R2 storage is not configured")
	}

	data, contentType, ext, err := extractImagePayload(c, "avatar")
	if err != nil {
		return problem(c, http.StatusBadRequest, "upload.invalid", "Invalid upload", err.Error())
	}

	key := fmt.Sprintf("users/%s/avatar_%d.%s", principal.UserID, time.Now().UnixNano(), ext)
	if err := s.storage.PutObject(c.Request().Context(), key, contentType, bytes.NewReader(data), int64(len(data))); err != nil {
		return problem(c, http.StatusInternalServerError, "storage.upload_failed", "Storage error", fmt.Sprintf("Failed to store in Cloudflare R2: %v", err))
	}

	publicURL := s.buildPublicAssetURL(c, key)

	_, err = s.pool.Exec(c.Request().Context(), `
		UPDATE users
		SET avatar_url = $1, updated_at = now()
		WHERE id = $2`, publicURL, principal.UserID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "database.update_failed", "Database error", err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{
		"avatarUrl": publicURL,
		"key":       key,
	})
}

func (s *Server) listLocations(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	rows, err := s.pool.Query(c.Request().Context(), `
		SELECT id::text, name, country_code, timezone, COALESCE(address_line_1, ''), COALESCE(city, ''), status
		FROM locations
		WHERE organization_id = $1
		ORDER BY created_at ASC`, principal.OrganizationID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "locations.lookup_failed", "Database error", err.Error())
	}
	defer rows.Close()

	type LocationItem struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		CountryCode  string `json:"countryCode"`
		Timezone     string `json:"timezone"`
		AddressLine1 string `json:"addressLine1"`
		City         string `json:"city"`
		Status       string `json:"status"`
	}

	items := make([]LocationItem, 0)
	for rows.Next() {
		var item LocationItem
		if err := rows.Scan(&item.ID, &item.Name, &item.CountryCode, &item.Timezone, &item.AddressLine1, &item.City, &item.Status); err == nil {
			items = append(items, item)
		}
	}

	return c.JSON(http.StatusOK, map[string]any{"data": items})
}

func (s *Server) createLocation(c echo.Context) error {
	principal, ok := identity.PrincipalFromContext(c.Request().Context())
	if !ok {
		return problem(c, http.StatusInternalServerError, "identity.context_missing", "Internal server error", "Authenticated identity context is missing")
	}

	var req CreateLocationRequest
	if err := c.Bind(&req); err != nil {
		return problem(c, http.StatusBadRequest, "request.invalid", "Invalid request", err.Error())
	}
	if strings.TrimSpace(req.Name) == "" {
		return problem(c, http.StatusUnprocessableEntity, "validation.name_required", "Name required", "Location name is required")
	}
	if req.CountryCode == "" {
		req.CountryCode = "UG"
	}
	if req.Timezone == "" {
		req.Timezone = "Africa/Kampala"
	}

	var newID string
	err := s.pool.QueryRow(c.Request().Context(), `
		INSERT INTO locations (organization_id, name, country_code, timezone, address_line_1, city)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text`, principal.OrganizationID, req.Name, req.CountryCode, req.Timezone, req.AddressLine1, req.City).Scan(&newID)
	if err != nil {
		return problem(c, http.StatusInternalServerError, "locations.create_failed", "Create failed", err.Error())
	}

	return c.JSON(http.StatusCreated, map[string]any{
		"id":           newID,
		"name":         req.Name,
		"countryCode":  req.CountryCode,
		"timezone":     req.Timezone,
		"addressLine1": req.AddressLine1,
		"city":         req.City,
		"status":       "active",
	})
}

func (s *Server) servePublicAsset(c echo.Context) error {
	if s.storage == nil {
		return c.String(http.StatusServiceUnavailable, "Storage is not available")
	}

	key := strings.TrimPrefix(c.Request().URL.Path, "/api/v1/public/assets/")
	key = strings.TrimPrefix(key, "/")
	if key == "" {
		return c.String(http.StatusBadRequest, "Asset key is required")
	}

	body, contentType, size, err := s.storage.GetObject(c.Request().Context(), key)
	if err != nil {
		return c.String(http.StatusNotFound, "Asset not found")
	}
	defer body.Close()

	c.Response().Header().Set("Content-Type", contentType)
	c.Response().Header().Set("Cache-Control", "public, max-age=604800, immutable")
	if size > 0 {
		c.Response().Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	_, err = io.Copy(c.Response().Writer, body)
	return err
}

func extractImagePayload(c echo.Context, formName string) (data []byte, contentType, ext string, err error) {
	// 1. Try JSON body with base64 data URL
	var body struct {
		DataURL string `json:"dataUrl"`
		Logo    string `json:"logoDataUrl"`
		Avatar  string `json:"avatarDataUrl"`
	}
	if err := c.Bind(&body); err == nil {
		raw := body.DataURL
		if raw == "" {
			raw = body.Logo
		}
		if raw == "" {
			raw = body.Avatar
		}
		if raw != "" && strings.HasPrefix(raw, "data:") {
			parts := strings.SplitN(raw, ",", 2)
			if len(parts) == 2 {
				header := parts[0]
				semi := strings.Index(header, ";")
				if semi > 5 {
					contentType = header[5:semi]
				}
				data, err = base64.StdEncoding.DecodeString(parts[1])
				if err == nil && len(data) > 0 {
					ext = extensionForMime(contentType)
					return data, contentType, ext, nil
				}
			}
		}
	}

	// 2. Try multipart file
	file, err := c.FormFile(formName)
	if err != nil {
		file, err = c.FormFile("file")
	}
	if err != nil {
		return nil, "", "", fmt.Errorf("no file or dataUrl provided")
	}

	src, err := file.Open()
	if err != nil {
		return nil, "", "", err
	}
	defer src.Close()

	buf, err := io.ReadAll(src)
	if err != nil {
		return nil, "", "", err
	}

	contentType = file.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(buf)
	}
	ext = strings.TrimPrefix(path.Ext(file.Filename), ".")
	if ext == "" {
		ext = extensionForMime(contentType)
	}

	return buf, contentType, ext, nil
}

func extensionForMime(mime string) string {
	switch mime {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/svg+xml":
		return "svg"
	default:
		return "png"
	}
}

func (s *Server) buildPublicAssetURL(c echo.Context, key string) string {
	proto := "https"
	host := c.Request().Host
	if host == "" || strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
		if os.Getenv("EASYHR_ENV") == "production" {
			return fmt.Sprintf("https://easyhr-api-180870038176.us-central1.run.app/api/v1/public/assets/%s", key)
		}
		return fmt.Sprintf("http://localhost:8080/api/v1/public/assets/%s", key)
	}
	if xfp := c.Request().Header.Get("X-Forwarded-Proto"); xfp != "" {
		proto = xfp
	}
	return fmt.Sprintf("%s://%s/api/v1/public/assets/%s", proto, host, key)
}
