package postgres

import "testing"

func TestParseAcquisitionLimits(t *testing.T) {
	for _, test := range []struct {
		attempts, retained string
		want               AcquisitionLimits
		invalid            bool
	}{
		{"", "", AcquisitionLimits{2, 4}, false},
		{"4", "8", AcquisitionLimits{4, 8}, false},
		{" 6 ", "12", AcquisitionLimits{6, 12}, false},
		{"0", "8", AcquisitionLimits{}, true},
		{"7", "8", AcquisitionLimits{}, true},
		{"4", "0", AcquisitionLimits{}, true},
		{"4", "13", AcquisitionLimits{}, true},
		{"unlimited", "8", AcquisitionLimits{}, true},
		{"4", "1.5", AcquisitionLimits{}, true},
	} {
		got, err := ParseAcquisitionLimits(test.attempts, test.retained)
		if (err != nil) != test.invalid || !test.invalid && got != test.want {
			t.Fatalf("Parse(%q,%q) = %+v, %v", test.attempts, test.retained, got, err)
		}
	}
	store := &Store{}
	if store.AcquisitionLimits() != (AcquisitionLimits{2, 4}) {
		t.Fatal("zero-value store changed defaults")
	}
	if err := store.ConfigureAcquisitionLimits(AcquisitionLimits{4, 8}); err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureAcquisitionLimits(AcquisitionLimits{99, 99}); err == nil {
		t.Fatal("unbounded configuration accepted")
	}
	if store.AcquisitionLimits() != (AcquisitionLimits{4, 8}) {
		t.Fatal("invalid configuration replaced valid limits")
	}
}
