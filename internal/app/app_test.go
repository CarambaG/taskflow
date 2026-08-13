package app

import (
	"reflect"
	"testing"

	"github.com/desertbit/closer/v4"
)

func TestRegisterShutdownHooks(t *testing.T) {
	lifecycle := closer.New()
	var calls []string

	registerShutdownHooks(
		lifecycle,
		func() error {
			calls = append(calls, "http")
			return nil
		},
		func() error {
			calls = append(calls, "dependencies")
			return nil
		},
	)

	if err := lifecycle.Close(); err != nil {
		t.Fatalf("close lifecycle: %v", err)
	}

	want := []string{"http", "dependencies"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected shutdown order: got %v, want %v", calls, want)
	}
}
