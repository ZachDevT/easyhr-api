package auth

import "testing"

func TestHasRequiredAudience(t *testing.T) {
	tests := []struct {
		name     string
		token    []string
		required []string
		want     bool
	}{
		{"no required audience", nil, nil, true},
		{"matching audience", []string{"easyhr-api"}, []string{"easyhr-api"}, true},
		{"one of several required audiences matches", []string{"mobile"}, []string{"web", "mobile"}, true},
		{"missing audience", []string{"other"}, []string{"easyhr-api"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasRequiredAudience(test.token, test.required); got != test.want {
				t.Fatalf("hasRequiredAudience() = %t, want %t", got, test.want)
			}
		})
	}
}
