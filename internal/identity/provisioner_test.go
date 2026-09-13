package identity

import (
	"errors"
	"testing"

	"github.com/clerk/clerk-sdk-go/v2"
)

func TestVerifiedUserProfile(t *testing.T) {
	firstName, lastName, primaryID := "Amina", "Hassan", "email_1"
	user := &clerk.User{FirstName: &firstName, LastName: &lastName, PrimaryEmailAddressID: &primaryID, EmailAddresses: []*clerk.EmailAddress{{ID: primaryID, EmailAddress: "amina@example.com", Verification: &clerk.Verification{Status: "verified"}}}}
	gotFirst, gotLast, gotEmail, err := verifiedUserProfile(user)
	if err != nil || gotFirst != firstName || gotLast != lastName || gotEmail != "amina@example.com" {
		t.Fatalf("verifiedUserProfile() = %q, %q, %q, %v", gotFirst, gotLast, gotEmail, err)
	}
}

func TestVerifiedUserProfileRejectsUnverifiedPrimaryEmail(t *testing.T) {
	firstName, lastName, primaryID := "Amina", "Hassan", "email_1"
	user := &clerk.User{FirstName: &firstName, LastName: &lastName, PrimaryEmailAddressID: &primaryID, EmailAddresses: []*clerk.EmailAddress{{ID: primaryID, EmailAddress: "amina@example.com", Verification: &clerk.Verification{Status: "unverified"}}}}
	if _, _, _, err := verifiedUserProfile(user); !errors.Is(err, ErrIdentityProfileIncomplete) {
		t.Fatalf("verifiedUserProfile() error = %v, want incomplete profile error", err)
	}
}
