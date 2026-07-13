package main

import (
	"testing"
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
