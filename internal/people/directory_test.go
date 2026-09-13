package people

import "testing"

func TestIsEmployeeStatus(t *testing.T) {
	if !isEmployeeStatus("active") || isEmployeeStatus("inactive") {
		t.Fatal("employee status validation returned an unexpected result")
	}
}
