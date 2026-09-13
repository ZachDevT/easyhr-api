package attendance

import "testing"

func TestPunchInputValidation(t *testing.T) {
	input := CreatePunchInput{Kind: "clock_in", OccurredAt: "2026-09-08T09:00:00Z", Source: "web"}
	if err := input.Validate(); err != nil {
		t.Fatalf("valid punch rejected: %v", err)
	}
	input.Kind = "delete"
	if err := input.Validate(); err == nil {
		t.Fatal("invalid punch kind accepted")
	}
}
