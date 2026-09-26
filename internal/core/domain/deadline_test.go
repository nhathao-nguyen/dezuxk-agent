package domain_test

import (
	"context"
	"testing"
	"time"

	"dezuxk-gateway/internal/core/domain"
)

func TestBoundContextKeepsShorterCallerDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	child, cancelChild := domain.BoundContext(parent, time.Second)
	defer cancelChild()
	parentDeadline, _ := parent.Deadline()
	childDeadline, ok := child.Deadline()
	if !ok || !parentDeadline.Equal(childDeadline) {
		t.Fatalf("child deadline = %v parent = %v", childDeadline, parentDeadline)
	}
}

func TestBoundContextCapsLongParent(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	child, cancelChild := domain.BoundContext(parent, 20*time.Second)
	defer cancelChild()
	deadline, ok := child.Deadline()
	if !ok {
		t.Fatal("missing deadline")
	}
	left := time.Until(deadline)
	if left <= 0 || left > 25*time.Second {
		t.Fatalf("left = %s", left)
	}
}
