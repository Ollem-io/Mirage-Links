package compensation

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRunOrderPresenceAndFirstError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps Steps
		want  []string
		err   bool
	}{
		{"none", Steps{}, nil, false},
		{"all", func() Steps { var x []string; _ = x; return Steps{} }(), nil, false},
	} {
		_ = tc
	}
	var got []string
	e1 := errors.New("route")
	e2 := errors.New("process")
	err := Run(context.Background(), Steps{
		RemoveRoute: func(context.Context) error { got = append(got, "route"); return e1 },
		StopProcess: func(context.Context) error { got = append(got, "process"); return e2 },
		ReleasePort: func(context.Context) error { got = append(got, "port"); return nil },
	})
	if !errors.Is(err, e1) || !reflect.DeepEqual(got, []string{"route", "process", "port"}) {
		t.Fatal(err, got)
	}
	got = nil
	if err = Run(context.Background(), Steps{StopProcess: func(context.Context) error { got = append(got, "process"); return nil }}); err != nil || !reflect.DeepEqual(got, []string{"process"}) {
		t.Fatal(err, got)
	}
}
