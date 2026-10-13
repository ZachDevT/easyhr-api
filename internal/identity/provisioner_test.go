package identity

import (
	"testing"

	"github.com/clerk/clerk-sdk-go/v2"
)

func TestExtractUserProfile(t *testing.T) {
	firstName, lastName, primaryID := "Amina", "Hassan", "email_1"
	user := &clerk.User{FirstName: &firstName, LastName: &lastName, PrimaryEmailAddressID: &primaryID, EmailAddresses: []*clerk.EmailAddress{{ID: primaryID, EmailAddress: "amina@example.com", Verification: &clerk.Verification{Status: "verified"}}}}
	gotFirst, gotLast, gotEmail := extractUserProfile(user)
	if gotFirst != firstName || gotLast != lastName || gotEmail != "amina@example.com" {
		t.Fatalf("extractUserProfile() = %q, %q, %q", gotFirst, gotLast, gotEmail)
	}
}

func TestExtractUserProfileFallbackToAnyEmail(t *testing.T) {
	firstName, lastName := "Amina", "Hassan"
	user := &clerk.User{FirstName: &firstName, LastName: &lastName, EmailAddresses: []*clerk.EmailAddress{{ID: "email_2", EmailAddress: "amina2@example.com"}}}
	gotFirst, gotLast, gotEmail := extractUserProfile(user)
	if gotFirst != firstName || gotLast != lastName || gotEmail != "amina2@example.com" {
		t.Fatalf("extractUserProfile() = %q, %q, %q", gotFirst, gotLast, gotEmail)
	}
}

