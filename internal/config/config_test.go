package config

import "testing"

func TestSplitCSVTrimsAndDropsEmptyItems(t *testing.T) {
	got := splitCSV(" https://one.example, ,https://two.example ,, ")
	want := []string{"https://one.example", "https://two.example"}
	if len(got) != len(want) {
		t.Fatalf("splitCSV() returned %d items, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("splitCSV()[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}
