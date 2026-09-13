package timeoff

import "testing"

func TestBalanceReservation(t *testing.T) {
	balance := Balance{AvailableMinutes: 960, ReservedMinutes: 480}
	if balance.UsableMinutes() != 480 {
		t.Fatalf("usable minutes = %d, want 480", balance.UsableMinutes())
	}
	if err := balance.CanReserve(481, false); err == nil {
		t.Fatal("expected insufficient balance error")
	}
	if err := balance.CanReserve(481, true); err != nil {
		t.Fatalf("negative policy should allow request: %v", err)
	}
}
