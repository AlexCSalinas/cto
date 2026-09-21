package main

import "testing"

func TestParseSeeds(t *testing.T) {
	got, err := parseSeeds("1, 2,42")
	if err != nil || len(got) != 3 || got[2] != 42 {
		t.Fatalf("parseSeeds = %v, %v", got, err)
	}
	if _, err := parseSeeds("1,x"); err == nil {
		t.Fatal("expected error for non-numeric seed")
	}
}

func TestFormatting(t *testing.T) {
	tests := []struct {
		from, to float64
		want     string
	}{
		{100, 50, "50% less"},
		{100, 130, "30% more"},
		{0, 5, "n/a"},
	}
	for _, tc := range tests {
		if got := pctChange(tc.from, tc.to); got != tc.want {
			t.Errorf("pctChange(%v, %v) = %q, want %q", tc.from, tc.to, got, tc.want)
		}
	}
	if fmtNum(0.4512) != "0.451" || fmtNum(1234.6) != "1235" || fmtNum(0) != "0" {
		t.Errorf("fmtNum: %q %q %q", fmtNum(0.4512), fmtNum(1234.6), fmtNum(0))
	}
}
