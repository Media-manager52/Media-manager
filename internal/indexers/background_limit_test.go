package indexers

import "testing"

func TestBackgroundQueryLimitIsSixtyPercentAndAtLeastOne(t *testing.T) {
	t.Parallel()
	for limit, want := range map[int]int{0: 1, 1: 1, 2: 1, 5: 3, 9: 5, 100: 60, 10000: 6000, 14000: 8400, 20000: 12000} {
		if got := BackgroundQueryLimit(limit); got != want {
			t.Fatalf("BackgroundQueryLimit(%d)=%d, want %d", limit, got, want)
		}
	}
	if limit, family := requestLimit(WorkloadBackground, Budget{QueryLimit: 1000, GrabLimit: 7}); limit != 600 || family != "query" {
		t.Fatalf("background limit=%d family=%s", limit, family)
	}
	if limit, _ := requestLimit(WorkloadInteractive, Budget{QueryLimit: 1000}); limit != 1000 {
		t.Fatalf("interactive searches keep the whole allowance, got %d", limit)
	}
}
