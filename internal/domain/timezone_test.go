package domain

import (
	"testing"
	"time"
	_ "time/tzdata"
)

func TestQuietHoursAcrossDaylightSavingChanges(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []struct {
		month  time.Month
		day    int
		offset int
	}{
		{time.March, 7, -4 * 3600},
		{time.October, 31, -5 * 3600},
	} {
		now := time.Date(2026, date.month, date.day, 23, 30, 0, 0, zone)
		cfg := DefaultPolicyConfig()
		if !cfg.inQuietHours(now) {
			t.Fatal("late evening is not quiet")
		}
		got := cfg.deferTarget(now)
		_, offset := got.Zone()
		if got.Day() != now.AddDate(0, 0, 1).Day() || got.Hour() != 7 || got.Minute() != 30 || offset != date.offset {
			t.Fatalf("defer %s -> %s (offset %d)", now, got, offset)
		}
		if cfg.inQuietHours(got) {
			t.Fatal("deferred reminder still quiet")
		}
	}
}
