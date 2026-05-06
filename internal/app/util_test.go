package app

import (
	"testing"
	"time"
)

func TestCutoffFromRetentionMonths(t *testing.T) {
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	got, err := cutoffFromRetention("3m", now)
	if err != nil {
		t.Fatal(err)
	}

	want := time.Date(2026, 2, 6, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestFormatFirefliesDate(t *testing.T) {
	value := time.Date(2026, 5, 6, 12, 34, 56, 789123000, time.FixedZone("CEST", 2*60*60))
	got := formatFirefliesDate(value)
	want := "2026-05-06T10:34:56.789Z"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
