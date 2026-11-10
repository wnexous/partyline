package schedules

import (
	"archive/zip"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// allZones lists the IANA zones of the Go distribution, the same database time/tzdata embeds.
func allZones(t *testing.T) []string {
	t.Helper()
	root, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Skipf("no go command: %v", err)
	}
	z, err := zip.OpenReader(filepath.Join(strings.TrimSpace(string(root)), "lib", "time", "zoneinfo.zip"))
	if err != nil {
		t.Skipf("no zone list: %v", err)
	}
	defer func() { _ = z.Close() }()
	var out []string
	for _, f := range z.File {
		if !strings.HasSuffix(f.Name, "/") {
			out = append(out, f.Name)
		}
	}
	require.Greater(t, len(out), 300)
	return out
}

// transitions returns, for the zone, two days before each change of its UTC offset in [from, to): the days
// where a local midnight or hour goes missing or repeats.
func transitions(loc *time.Location, from, to time.Time) []time.Time {
	var out []time.Time
	_, prev := from.In(loc).Zone()
	for t := from; t.Before(to); t = t.Add(3 * time.Hour) {
		if _, off := t.In(loc).Zone(); off != prev {
			out = append(out, t.Add(-48*time.Hour))
			prev = off
		}
	}
	return out
}

// HIGH regression (zones whose local midnight is missing or repeated on a daylight saving day, such as
// America/Havana, Atlantic/Azores or America/Santiago, with a weekday cron): in every zone, for every
// expression, around every offset change of a year, Next ends, moves strictly forward and lands on a time the
// expression matches on the zone's wall clock.
func TestCronNextTerminatesAndAdvancesInEveryZone(t *testing.T) {
	exprs := []string{
		"0 9 * * 1-5", "0 9 * * 2", "0 9 * * 0", "0 9 * * 6", "0 9 1 * *", "*/15 * * * *", "0 0 * * *", "30 2 * * *",
		"0 0 31 2 *", "0 0 29 2 *", "59 23 * * 1", "0,30 0 1 * 1", "0 0 */2 * *", "0 0 1 1 *", "15 0 1-31/7 * 6-7",
		"0 23 * * *", "30 0 * * *", "0 0 30 2 0", "0 0 * * 1-5", "0 1 * * *",
	}
	crons := make([]*Cron, len(exprs))
	for i, e := range exprs {
		crons[i] = mustCron(t, e)
	}
	from, to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, name := range allZones(t) {
			loc, err := time.LoadLocation(name)
			if err != nil {
				continue
			}
			bases := append([]time.Time{from, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}, transitions(loc, from, to)...)
			for i, c := range crons {
				for _, base := range bases {
					cur := base
					for range 12 {
						next, err := c.NextChecked(cur, loc)
						if err != nil {
							t.Errorf("%s %q from %v: %v", name, exprs[i], cur, err)
							break
						}
						if next.IsZero() {
							break
						}
						if !next.After(cur) {
							t.Errorf("%s %q: %v is not after %v", name, exprs[i], next, cur)
							break
						}
						l := next.In(loc)
						if !has(c.minute, l.Minute()) || !has(c.hour, l.Hour()) || !has(c.month, int(l.Month())) || !c.dayMatches(l) {
							t.Errorf("%s %q: %v does not match", name, exprs[i], l)
							break
						}
						cur = next
					}
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Minute):
		t.Fatal("Next did not end")
	}
}
