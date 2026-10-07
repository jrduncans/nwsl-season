package lifetime

import (
	"context"
	"errors"
	"testing"
	"time"
)

type valueKey struct{}

func TestDetachIgnoresCallerDeadlineButHonorsOwner(t *testing.T) {
	owner, stopOwner := context.WithCancel(context.Background())
	defer stopOwner()
	caller, expireCaller := context.WithTimeout(context.WithValue(Root(owner), valueKey{}, "kept"), time.Hour)
	detached, cancel := Detach(caller)
	defer cancel()

	if _, ok := detached.Deadline(); ok {
		t.Fatal("detached context retained the caller deadline")
	}
	if got := detached.Value(valueKey{}); got != "kept" {
		t.Fatalf("value = %v, want kept", got)
	}
	expireCaller()
	if err := detached.Err(); err != nil {
		t.Fatalf("caller cancellation reached detached work: %v", err)
	}
	stopOwner()
	select {
	case <-detached.Done():
	case <-time.After(time.Second):
		t.Fatal("owner shutdown did not cancel detached work")
	}
	if !errors.Is(detached.Err(), context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", detached.Err())
	}
}

func TestDetachFromStoppedOwnerIsCanceled(t *testing.T) {
	owner, stopOwner := context.WithCancel(context.Background())
	stopOwner()
	detached, cancel := Detach(Root(owner))
	defer cancel()
	select {
	case <-detached.Done():
	case <-time.After(time.Second):
		t.Fatal("detaching beneath a stopped owner was not canceled")
	}
}

func TestDetachWithoutOwnerMatchesWithoutCancel(t *testing.T) {
	caller, cancelCaller := context.WithCancel(context.Background())
	detached, cancel := Detach(caller)
	cancelCaller()
	if err := detached.Err(); err != nil {
		t.Fatalf("detached work without an owner was canceled: %v", err)
	}
	cancel()
	if !errors.Is(detached.Err(), context.Canceled) {
		t.Fatalf("CancelFunc did not cancel: %v", detached.Err())
	}
}
