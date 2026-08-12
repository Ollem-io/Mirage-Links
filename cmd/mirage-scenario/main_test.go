package main

import (
	"github.com/primeintellect/mirage/internal/application/ports"
	"testing"
)

func TestRunScenario(t *testing.T) {
	if err := run(); err != nil {
		t.Fatal(err)
	}
}

func TestTracePorts(t *testing.T) {
	tr := trace{}
	if ok, err := tr.Alive(nil, ports.ProcessIdentity{}); err != nil || !ok {
		t.Fatalf("alive=%v err=%v", ok, err)
	}
	if routes, err := tr.List(nil); err != nil || routes != nil {
		t.Fatalf("routes=%v err=%v", routes, err)
	}
	if err := tr.Reconcile(nil, nil); err != nil {
		t.Fatal(err)
	}
	if logs, err := tr.Tail(nil, "", 1); err != nil || logs != nil {
		t.Fatalf("logs=%v err=%v", logs, err)
	}
	if r, err := tr.Follow(nil, ""); err != nil || r != nil {
		t.Fatalf("reader=%v err=%v", r, err)
	}
}
