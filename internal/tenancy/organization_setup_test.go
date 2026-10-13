package tenancy

import "testing"

func validOrganizationSetup() OrganizationSetupInput {
	return OrganizationSetupInput{
		LegalName:          "Acacia Technologies Limited",
		DisplayName:        "Acacia Labs",
		CountryCode:        "UG",
		Timezone:           "Africa/Kampala",
		CurrencyCode:       "UGX",
		WebsiteURL:         "https://acacia.example",
		ContactEmail:       "hello@acacia.example",
		Subdomain:          "acacia-labs",
		BrandColor:         "#A238FF",
		SocialLinks:        map[string]string{"linkedin": "https://www.linkedin.com/company/acacia"},
		InitialDepartments: []string{"Engineering", "People"},
		Headquarters: LocationInput{
			Name:        "Kampala HQ",
			CountryCode: "UG",
			Timezone:    "Africa/Kampala",
			Geofence: &GeofenceInput{
				Latitude: 0.3136, Longitude: 32.5811, RadiusMeters: 300, AccuracyToleranceMeters: 50,
			},
		},
	}
}

func TestOrganizationSetupValidationAcceptsPrototypeCompatibleInput(t *testing.T) {
	if err := validOrganizationSetup().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestOrganizationSetupValidationRejectsUnsafeWorkspaceAndGeofenceValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*OrganizationSetupInput)
	}{
		{"invalid subdomain", func(input *OrganizationSetupInput) { input.Subdomain = "Acacia Labs" }},
		{"invalid color", func(input *OrganizationSetupInput) { input.BrandColor = "purple" }},
		{"unsupported country", func(input *OrganizationSetupInput) { input.CountryCode = "ZZ" }},
		{"bad geofence", func(input *OrganizationSetupInput) { input.Headquarters.Geofence.Latitude = 99 }},
		{"duplicate departments", func(input *OrganizationSetupInput) { input.InitialDepartments = []string{"People", "people"} }},
		{"reserved api subdomain", func(input *OrganizationSetupInput) { input.Subdomain = "api" }},
		{"reserved app subdomain", func(input *OrganizationSetupInput) { input.Subdomain = "app" }},
		{"reserved admin subdomain", func(input *OrganizationSetupInput) { input.Subdomain = "admin" }},
		{"reserved www subdomain", func(input *OrganizationSetupInput) { input.Subdomain = "www" }},
		{"reserved clerk subdomain", func(input *OrganizationSetupInput) { input.Subdomain = "clerk" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validOrganizationSetup()
			test.mutate(&input)
			if err := input.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
		})
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Acacia Labs", "acacia-labs"},
		{"Acme & Sons Ltd.", "acme-sons-ltd"},
		{"---hello---world---", "hello-world"},
		{"   ", "workspace"},
		{"Uganda HR Pro 2026", "uganda-hr-pro-2026"},
	}
	for _, c := range cases {
		got := Slugify(c.input)
		if got != c.expected {
			t.Errorf("Slugify(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}
