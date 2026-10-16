package cron_test

import (
	"testing"
	"time"

	"github.com/d31ma/heimdall/app/server/utils/cron"
)

func TestNextFiringTimes(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("no tzdata on this host: %v", err)
	}

	for name, c := range map[string]struct {
		expression string
		from       time.Time
		location   *time.Location
		want       time.Time
	}{
		"nightly": {
			"0 3 * * *",
			time.Date(2026, 8, 17, 4, 0, 0, 0, time.UTC), time.UTC,
			time.Date(2026, 8, 18, 3, 0, 0, 0, time.UTC),
		},
		"every fifteen minutes": {
			"*/15 * * * *",
			time.Date(2026, 8, 17, 4, 1, 0, 0, time.UTC), time.UTC,
			time.Date(2026, 8, 17, 4, 15, 0, 0, time.UTC),
		},
		"weekdays only": {
			"30 6 * * MON-FRI",
			// A Saturday: the next run is Monday, not tomorrow.
			time.Date(2026, 8, 15, 7, 0, 0, 0, time.UTC), time.UTC,
			time.Date(2026, 8, 17, 6, 30, 0, 0, time.UTC),
		},
		"first of the month": {
			"0 0 1 * *",
			time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), time.UTC,
			time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		},
		"a named timezone is honoured, not the host's": {
			"0 3 * * *",
			time.Date(2026, 8, 17, 4, 0, 0, 0, time.UTC), berlin,
			// 03:00 Berlin on the 18th is 01:00 UTC.
			time.Date(2026, 8, 18, 1, 0, 0, 0, time.UTC),
		},
	} {
		t.Run(name, func(t *testing.T) {
			expression, err := cron.Parse(c.expression)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := expression.Next(c.from, c.location)
			if !got.Equal(c.want) {
				t.Fatalf("next after %s = %s, want %s", c.from, got, c.want)
			}
		})
	}
}

// TestImpossibleScheduleNeverFires: 31 February must return "never" rather
// than spin looking for it.
func TestImpossibleScheduleNeverFires(t *testing.T) {
	expression, err := cron.Parse("0 0 31 2 *")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if next := expression.Next(time.Now(), time.UTC); !next.IsZero() {
		t.Fatalf("31 February fires at %s", next)
	}
}

func TestBadExpressionsAreRefused(t *testing.T) {
	for _, expression := range []string{
		"0 3 * *", "0 0 3 * * *", "0 99 * * *", "@daily", "0 3 * * FUNDAY", "*/0 * * * *", "10-5 * * * *",
	} {
		if _, err := cron.Parse(expression); err == nil {
			t.Errorf("%q parsed successfully", expression)
		}
	}
}
