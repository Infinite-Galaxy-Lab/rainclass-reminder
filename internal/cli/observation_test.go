package cli

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestObservationIsOptInAndDefersUntilStart(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "observations")
	t.Setenv("YKT_OBSERVE_DIR", output)
	for _, start := range []string{"", "9999-01-01T00:00:00Z"} {
		t.Setenv("YKT_OBSERVE_FROM", start)
		observer, err := openObservation(dir, nil, slog.Default())
		if err != nil || observer != nil {
			t.Fatalf("observation must remain disabled: %v, %v", observer, err)
		}
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("disabled observer created output")
	}
	t.Setenv("YKT_OBSERVE_FROM", "invalid")
	if _, err := openObservation(dir, nil, slog.Default()); err == nil {
		t.Fatal("invalid start time accepted")
	}
	t.Setenv("YKT_OBSERVE_FROM", "2000-01-01T00:00:00Z")
	observer, err := openObservation(dir, nil, slog.Default())
	if err != nil || observer == nil {
		t.Fatalf("observer did not start: %v", err)
	}
	_ = observer.recorder.Close()
}
