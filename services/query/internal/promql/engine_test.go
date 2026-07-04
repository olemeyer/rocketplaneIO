package promql

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInstantNotImplemented(t *testing.T) {
	_, err := NewEngine().Instant(context.Background(), "up", time.Time{})
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}

func TestRangeNotImplemented(t *testing.T) {
	_, err := NewEngine().Range(context.Background(), "up", time.Time{}, time.Time{}, time.Minute)
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}
