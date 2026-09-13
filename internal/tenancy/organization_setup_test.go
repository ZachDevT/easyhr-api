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
