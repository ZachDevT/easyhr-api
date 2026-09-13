package tenancy

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	subdomainPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	brandColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	currencyPattern   = regexp.MustCompile(`^[A-Z]{3}$`)
)

var supportedRegistrationCountries = map[string]struct{}{
	"UG": {}, "KE": {}, "RW": {}, "TZ": {}, "US": {}, "GB": {},
}

type OrganizationSetupInput struct {
	LegalName          string
	DisplayName        string
	RegistrationNumber string
	TaxIdentifier      string
	CountryCode        string
	Timezone           string
	CurrencyCode       string
	Industry           string
	CompanySizeBand    string
	WebsiteURL         string
	ContactEmail       string
	ContactPhone       string
	Address            AddressInput
	Subdomain          string
	BrandColor         string
	SocialLinks        map[string]string
	InitialDepartments []string
	Headquarters       LocationInput
}

type AddressInput struct {
	Line1              string
	Line2              string
	City               string
	AdministrativeArea string
	PostalCode         string
}

type LocationInput struct {
	Name        string
	CountryCode string
	Timezone    string
	Address     AddressInput
	Geofence    *GeofenceInput
}

type GeofenceInput struct {
	Latitude                float64
	Longitude               float64
	RadiusMeters            int
	AccuracyToleranceMeters int
}

func (input OrganizationSetupInput) Validate() error {
	if err := validateRequired("legalName", input.LegalName, 200); err != nil {
		return err
	}
	if err := validateRequired("displayName", input.DisplayName, 200); err != nil {
		return err
	}
	if _, ok := supportedRegistrationCountries[input.CountryCode]; !ok {
		return fmt.Errorf("countryCode is not currently supported")
	}
	if !currencyPattern.MatchString(input.CurrencyCode) {
		return fmt.Errorf("currencyCode must be an ISO 4217 uppercase code")
	}
	if _, err := time.LoadLocation(input.Timezone); err != nil {
		return fmt.Errorf("timezone must be a valid IANA timezone")
	}
	if input.ContactEmail != "" {
		if _, err := mail.ParseAddress(input.ContactEmail); err != nil {
			return fmt.Errorf("contactEmail must be valid")
		}
	}
	if input.WebsiteURL != "" {
		parsed, err := url.ParseRequestURI(input.WebsiteURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("websiteUrl must be a valid HTTPS URL")
		}
	}
	if !subdomainPattern.MatchString(input.Subdomain) {
		return fmt.Errorf("subdomain must be 1-63 lowercase letters, numbers, or internal hyphens")
	}
	if !brandColorPattern.MatchString(input.BrandColor) {
		return fmt.Errorf("brandColor must be a #RRGGBB color")
	}
	for platform, link := range input.SocialLinks {
		if !isSupportedSocialPlatform(platform) {
			return fmt.Errorf("socialLinks contains unsupported platform %q", platform)
		}
		if link == "" {
			return fmt.Errorf("socialLinks.%s must not be empty", platform)
		}
		parsed, err := url.ParseRequestURI(link)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return fmt.Errorf("socialLinks.%s must be a valid HTTPS URL", platform)
		}
	}
	if len(input.InitialDepartments) > 50 {
		return fmt.Errorf("initialDepartments cannot contain more than 50 departments")
	}
	seenDepartments := make(map[string]struct{}, len(input.InitialDepartments))
	for _, department := range input.InitialDepartments {
		if err := validateRequired("initialDepartments", department, 160); err != nil {
			return err
		}
		key := strings.ToLower(strings.TrimSpace(department))
		if _, exists := seenDepartments[key]; exists {
			return fmt.Errorf("initialDepartments must not contain duplicates")
		}
		seenDepartments[key] = struct{}{}
	}
	return input.Headquarters.validate(input.CountryCode, input.Timezone)
}

func (input LocationInput) validate(organizationCountry, organizationTimezone string) error {
	if input.Name == "" {
		return nil
	}
	if err := validateRequired("headquarters.name", input.Name, 160); err != nil {
		return err
	}
	if input.CountryCode != organizationCountry {
		return fmt.Errorf("headquarters.countryCode must match countryCode during initial setup")
	}
	if input.Timezone != organizationTimezone {
		return fmt.Errorf("headquarters.timezone must match timezone during initial setup")
	}
	if input.Geofence == nil {
		return nil
	}
	geofence := input.Geofence
	if geofence.Latitude < -90 || geofence.Latitude > 90 || geofence.Longitude < -180 || geofence.Longitude > 180 {
		return fmt.Errorf("headquarters.geofence coordinates are out of range")
	}
	if geofence.RadiusMeters < 10 || geofence.RadiusMeters > 100000 {
		return fmt.Errorf("headquarters.geofence.radiusMeters must be between 10 and 100000")
	}
	if geofence.AccuracyToleranceMeters < 0 || geofence.AccuracyToleranceMeters > 1000 {
		return fmt.Errorf("headquarters.geofence.accuracyToleranceMeters must be between 0 and 1000")
	}
	return nil
}

func validateRequired(field, value string, maxLength int) error {
	length := len(strings.TrimSpace(value))
	if length == 0 || length > maxLength {
		return fmt.Errorf("%s must contain between 1 and %d characters", field, maxLength)
	}
	return nil
}

func isSupportedSocialPlatform(platform string) bool {
	switch platform {
	case "linkedin", "x", "facebook", "tiktok", "glassdoor":
		return true
	default:
		return false
	}
}
