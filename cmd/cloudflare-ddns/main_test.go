package main

import (
	"testing"
	"time"
)

func TestBoolEnv(t *testing.T) {
	t.Setenv("TEST_BOOL", "")
	value, err := boolEnv("TEST_BOOL")
	if err != nil || value {
		t.Fatalf("unset bool = %t, err = %v", value, err)
	}

	t.Setenv("TEST_BOOL", "true")
	value, err = boolEnv("TEST_BOOL")
	if err != nil || !value {
		t.Fatalf("true bool = %t, err = %v", value, err)
	}

	t.Setenv("TEST_BOOL", "definitely")
	if _, err := boolEnv("TEST_BOOL"); err == nil {
		t.Fatal("invalid bool was accepted")
	}
}

func TestFormatLastPing(t *testing.T) {
	now := time.Date(2026, time.July, 13, 20, 0, 0, 0, time.UTC)
	zero := time.Time{}
	tests := []struct {
		name     string
		lastPing *time.Time
		want     string
	}{
		{name: "never", want: "never"},
		{name: "zero", lastPing: &zero, want: "never"},
		{name: "future", lastPing: timePointer(now.Add(time.Minute)), want: "just now"},
		{name: "subsecond", lastPing: timePointer(now.Add(-500 * time.Millisecond)), want: "just now"},
		{name: "seconds", lastPing: timePointer(now.Add(-42 * time.Second)), want: "42s ago"},
		{name: "minutes", lastPing: timePointer(now.Add(-12*time.Minute - 30*time.Second)), want: "12m ago"},
		{name: "hours", lastPing: timePointer(now.Add(-3*time.Hour - 45*time.Minute)), want: "3h ago"},
		{name: "days", lastPing: timePointer(now.Add(-5*time.Hour - 48*time.Hour)), want: "2d ago"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatLastPing(now, test.lastPing); got != test.want {
				t.Fatalf("formatLastPing() = %q, want %q", got, test.want)
			}
		})
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
