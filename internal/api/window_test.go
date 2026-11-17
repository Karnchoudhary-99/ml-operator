package api

import (
	"strings"
	"testing"
	"time"
)

// 2026-11-16 is a Monday; the tests below anchor on it so the
// weekday arithmetic is checkable by hand.
func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestMaintenanceWindowActiveAt(t *testing.T) {
	mon0900 := mustTime("2026-11-16T09:00:00Z")
	cases := []struct {
		name string
		w    *MaintenanceWindowSpec
		at   time.Time
		want bool
	}{
		{"nil window always open", nil, mon0900, true},
		{"inside daytime window", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "08:00", End: "10:00"}, mon0900, true},
		{"before window opens", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "08:00", End: "10:00"}, mustTime("2026-11-16T07:59:00Z"), false},
		{"at closing time the window is shut", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "08:00", End: "10:00"}, mustTime("2026-11-16T10:00:00Z"), false},
		{"at opening time the window is open", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "08:00", End: "10:00"}, mustTime("2026-11-16T08:00:00Z"), true},
		{"wrong weekday", &MaintenanceWindowSpec{Days: []string{"Tue"}, Start: "08:00", End: "10:00"}, mon0900, false},
		{"empty days means every day", &MaintenanceWindowSpec{Start: "08:00", End: "10:00"}, mon0900, true},
		{"empty days on a Sunday too", &MaintenanceWindowSpec{Start: "08:00", End: "10:00"}, mustTime("2026-11-22T09:00:00Z"), true},
		{"day abbreviations are case-insensitive", &MaintenanceWindowSpec{Days: []string{"mon"}, Start: "08:00", End: "10:00"}, mon0900, true},
		{"overnight window before midnight", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "22:00", End: "02:00"}, mustTime("2026-11-16T23:30:00Z"), true},
		{"overnight window after midnight belongs to Monday", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "22:00", End: "02:00"}, mustTime("2026-11-17T01:30:00Z"), true},
		{"overnight window does not open on Tuesday itself", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "22:00", End: "02:00"}, mustTime("2026-11-17T23:30:00Z"), false},
		{"overnight window closes at 02:00", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "22:00", End: "02:00"}, mustTime("2026-11-17T02:00:00Z"), false},
		{"overnight window before it opens", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "22:00", End: "02:00"}, mustTime("2026-11-16T21:59:00Z"), false},
		{"invalid day never opens", &MaintenanceWindowSpec{Days: []string{"Funday"}, Start: "08:00", End: "10:00"}, mon0900, false},
		{"invalid time never opens", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "25:00", End: "10:00"}, mon0900, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.w.ActiveAt(tc.at); got != tc.want {
				t.Fatalf("ActiveAt(%v) = %v, want %v", tc.at, got, tc.want)
			}
		})
	}
}

func TestMaintenanceWindowNextOpening(t *testing.T) {
	cases := []struct {
		name string
		w    *MaintenanceWindowSpec
		at   time.Time
		want string
	}{
		{"same day later", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "08:00", End: "10:00"}, mustTime("2026-11-16T07:00:00Z"), "2026-11-16T08:00:00Z"},
		{"next week when today's window already passed", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "08:00", End: "10:00"}, mustTime("2026-11-16T11:00:00Z"), "2026-11-23T08:00:00Z"},
		{"tomorrow's day", &MaintenanceWindowSpec{Days: []string{"Tue"}, Start: "02:00", End: "04:00"}, mustTime("2026-11-16T09:00:00Z"), "2026-11-17T02:00:00Z"},
		{"daily window opens tomorrow", &MaintenanceWindowSpec{Start: "02:00", End: "04:00"}, mustTime("2026-11-16T09:00:00Z"), "2026-11-17T02:00:00Z"},
		{"overnight window opens tonight", &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "22:00", End: "02:00"}, mustTime("2026-11-16T09:00:00Z"), "2026-11-16T22:00:00Z"},
		{"nil window has no opening", nil, mustTime("2026-11-16T09:00:00Z"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.w.NextOpening(tc.at)
			if tc.want == "" {
				if !got.IsZero() {
					t.Fatalf("NextOpening = %v, want zero time", got)
				}
				return
			}
			if got.Format(time.RFC3339) != tc.want {
				t.Fatalf("NextOpening = %v, want %v", got.Format(time.RFC3339), tc.want)
			}
		})
	}
}

func TestMaintenanceWindowDescribe(t *testing.T) {
	cases := []struct {
		w    *MaintenanceWindowSpec
		want string
	}{
		{nil, ""},
		{&MaintenanceWindowSpec{Start: "02:00", End: "04:00"}, "daily 02:00–04:00 UTC"},
		{&MaintenanceWindowSpec{Days: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, Start: "02:00", End: "04:00"}, "Mon–Fri 02:00–04:00 UTC"},
		{&MaintenanceWindowSpec{Days: []string{"Sat", "Sun"}, Start: "22:00", End: "02:00"}, "Sat–Sun 22:00–02:00 UTC"},
		{&MaintenanceWindowSpec{Days: []string{"Mon", "Wed", "Fri"}, Start: "02:00", End: "04:00"}, "Mon, Wed, Fri 02:00–04:00 UTC"},
		{&MaintenanceWindowSpec{Days: []string{"Tue"}, Start: "02:00", End: "04:00"}, "Tue 02:00–04:00 UTC"},
	}
	for _, tc := range cases {
		if got := tc.w.Describe(); got != tc.want {
			t.Fatalf("Describe() = %q, want %q", got, tc.want)
		}
	}
}

func TestValidateMaintenanceWindow(t *testing.T) {
	cases := []struct {
		name string
		w    *MaintenanceWindowSpec
		want string // "" means valid
	}{
		{"nil is valid", nil, ""},
		{"weekday window", &MaintenanceWindowSpec{Days: []string{"Mon", "Fri"}, Start: "02:00", End: "04:00"}, ""},
		{"daily overnight window", &MaintenanceWindowSpec{Start: "22:00", End: "02:00"}, ""},
		{"lowercase day", &MaintenanceWindowSpec{Days: []string{"mon"}, Start: "02:00", End: "04:00"}, ""},
		{"bad day", &MaintenanceWindowSpec{Days: []string{"Funday"}, Start: "02:00", End: "04:00"}, "maintenanceWindow.days[0]"},
		{"full day name rejected", &MaintenanceWindowSpec{Days: []string{"Monday"}, Start: "02:00", End: "04:00"}, "maintenanceWindow.days[0]"},
		{"start not HH:MM", &MaintenanceWindowSpec{Start: "2pm", End: "04:00"}, "maintenanceWindow.start"},
		{"start hour 25", &MaintenanceWindowSpec{Start: "25:00", End: "04:00"}, "maintenanceWindow.start"},
		{"end minute 60", &MaintenanceWindowSpec{Start: "02:00", End: "04:60"}, "maintenanceWindow.end"},
		{"start equals end", &MaintenanceWindowSpec{Start: "02:00", End: "02:00"}, "equals end"},
		{"one minute window is valid", &MaintenanceWindowSpec{Start: "02:00", End: "02:01"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := validDeployment()
			d.Spec.MaintenanceWindow = tc.w
			errs := Validate(d)
			joined := ""
			for _, err := range errs {
				joined += err.Error() + "; "
			}
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("expected valid, got errors: %s", joined)
				}
				return
			}
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("expected error containing %q, got %q", tc.want, joined)
			}
		})
	}
}

func TestLintMaintenanceWindow(t *testing.T) {
	d := validDeployment()
	d.Spec.MaintenanceWindow = &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "02:00", End: "02:10"}
	warns := Lint(d)
	joined := strings.Join(warns, "; ")
	if !strings.Contains(joined, "only 10 minutes long") {
		t.Fatalf("expected short-window warning, got %q", joined)
	}

	d.Spec.MaintenanceWindow = &MaintenanceWindowSpec{Days: []string{"Mon"}, Start: "02:00", End: "04:00"}
	if warns := Lint(d); len(warns) != 0 {
		t.Fatalf("two-hour window should not warn, got %v", warns)
	}
}
