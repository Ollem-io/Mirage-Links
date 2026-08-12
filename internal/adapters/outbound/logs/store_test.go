package logs

import (
	"context"
	"github.com/primeintellect/mirage/internal/application/ports"
	"github.com/primeintellect/mirage/internal/domain"
	"io"
	"strings"
	"testing"
	"time"
)

func TestTailLabelsRedactionAndClose(t *testing.T) {
	s := NewStore("DATABASE_PASSWORD=abc")
	id := domain.LinkID("l")
	s.now = func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) }
	for _, e := range []ports.LogEntry{{Stream: "stderr", Text: "mir_abcdef Bearer top DATABASE_PASSWORD=abc"}, {Stream: "invalid", Text: "new"}} {
		if e := s.Append(context.Background(), id, e); e != nil {
			t.Fatal(e)
		}
	}
	got, e := s.Tail(context.Background(), id, 9)
	if e != nil || len(got) != 2 {
		t.Fatal(got, e)
	}
	if got[0].Stream != "stderr" || got[1].Stream != "stdout" || got[0].At.IsZero() || strings.Contains(got[0].Text, "mir_") || strings.Contains(got[0].Text, "abc") {
		t.Fatalf("%+v", got)
	}
	if e := s.Close(id); e != nil {
		t.Fatal(e)
	}
	if e := s.Append(context.Background(), id, ports.LogEntry{}); e != io.ErrClosedPipe {
		t.Fatalf("%v", e)
	}
}
func TestFollowCancellationAndProcessClose(t *testing.T) {
	s := NewStore()
	id := domain.LinkID("l")
	ctx, cancel := context.WithCancel(context.Background())
	r, e := s.Follow(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error)
	go func() { b := make([]byte, 10); _, e := r.Read(b); done <- e }()
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("reader leaked")
	}
	r, _ = s.Follow(context.Background(), domain.LinkID("x"))
	if e := s.Close("x"); e != nil {
		t.Fatal(e)
	}
	b := make([]byte, 1)
	if _, e := r.Read(b); e != io.EOF {
		t.Fatalf("%v", e)
	}
}
func TestRingRetainsNewestCompleteRecords(t *testing.T) {
	s := NewStore()
	id := domain.LinkID("l") // force overflow with enough whole sizeable records
	text := strings.Repeat("x", 1024*1024)
	for i := 0; i < 12; i++ {
		if e := s.Append(context.Background(), id, ports.LogEntry{Text: text + string(rune('a'+i))}); e != nil {
			t.Fatal(e)
		}
	}
	if s.Bytes(id) > Capacity {
		t.Fatal(s.Bytes(id))
	}
	got, _ := s.Tail(context.Background(), id, 100)
	if len(got) >= 12 || len(got) == 0 || !strings.HasSuffix(got[len(got)-1].Text, "l") {
		t.Fatalf("len=%d last=%q", len(got), got[len(got)-1].Text[len(got[len(got)-1].Text)-1:])
	}
	if e := s.Append(context.Background(), id, ports.LogEntry{Text: strings.Repeat("z", Capacity+1)}); e != nil {
		t.Fatal(e)
	}
	if s.Bytes(id) > Capacity {
		t.Fatal("overflow")
	}
}
func TestCanceledInputs(t *testing.T) {
	s := NewStore()
	ctx, c := context.WithCancel(context.Background())
	c()
	if e := s.Append(ctx, "x", ports.LogEntry{}); e == nil {
		t.Fatal("append")
	}
	if _, e := s.Tail(ctx, "x", 1); e == nil {
		t.Fatal("tail")
	}
	if _, e := s.Follow(ctx, "x"); e == nil {
		t.Fatal("follow")
	}
}
