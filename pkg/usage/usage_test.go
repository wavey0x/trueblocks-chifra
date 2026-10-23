package usage

import (
	"errors"
	"testing"
)

func TestUsageErrorPreservesMessage(t *testing.T) {
	err := Usage("x {0}", "y")
	if err.Error() != "x y" {
		t.Fatalf("Usage() = %q, want %q", err.Error(), "x y")
	}
}

func TestUsageIs(t *testing.T) {
	if !Is(Usage("x")) {
		t.Fatal("expected usage error to match")
	}
	if Is(errors.New("x")) {
		t.Fatal("plain errors should not match usage errors")
	}
}
