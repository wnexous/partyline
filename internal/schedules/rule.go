package schedules

import (
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"strconv"
	"strings"
	"time"

	// The server runs on a distroless image without a zoneinfo database: the schedules' timezones come from
	// the binary (HB-01).
	_ "time/tzdata"
)

// Cron is a parsed standard 5-field cron expression (minute hour day-of-month month day-of-week), the
// syntax of Vixie cron: lists (1,2), ranges (1-5), steps (*/15, 1-30/5, 5/10), month and weekday names
// (JAN, MON), 7 for Sunday, "?" for "*", and the descriptors @yearly, @annually, @monthly, @weekly,
// @daily, @midnight and @hourly. When both day fields are restricted a day matches either of them, as in
// Vixie cron.
type Cron struct {
	minute, hour, dom, month, dow uint64
	// domStar and dowStar are set when the day field starts with "*" (or "?"): the other day field alone
	// then decides.
	domStar, dowStar bool
}

var cronDescriptors = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var (
	monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	dayNames   = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
)

// ParseCron parses a standard 5-field cron expression or one of the descriptors.
func ParseCron(expr string) (*Cron, error) {
	expr = strings.TrimSpace(expr)
	if d, ok := cronDescriptors[strings.ToLower(expr)]; ok {
		expr = d
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, errors.New("must have 5 fields: minute hour day-of-month month day-of-week")
	}
	c := &Cron{}
	var err error
	if c.minute, _, err = parseField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if c.hour, _, err = parseField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if c.dom, c.domStar, err = parseField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if c.month, _, err = parseField(fields[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if c.dow, c.dowStar, err = parseField(fields[4], 0, 7, dayNames); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	if c.dow&(1<<7) != 0 { // 7 is Sunday too
		c.dow = c.dow&^(1<<7) | 1
	}
	return c, nil
}

// parseField parses one comma separated field whose values lie in [lo, hi]. star reports whether the field
// starts with "*" or "?".
func parseField(field string, lo, hi int, names map[string]int) (set uint64, star bool, err error) {
	star = strings.HasPrefix(field, "*") || strings.HasPrefix(field, "?")
	for _, part := range strings.Split(field, ",") {
		bitsOf, err := parsePart(part, lo, hi, names)
		if err != nil {
			return 0, false, err
		}
		set |= bitsOf
	}
	return set, star, nil
}

func parsePart(part string, lo, hi int, names map[string]int) (uint64, error) {
	if part == "" {
		return 0, errors.New("empty value")
	}
	rangePart, stepText, hasStep := strings.Cut(part, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepText)
		if err != nil || n < 1 || n > hi-lo+1 {
			return 0, fmt.Errorf("bad step %q", stepText)
		}
		step = n
	}
	var from, to int
	switch rangePart {
	case "*", "?":
		from, to = lo, hi
	default:
		a, b, isRange := strings.Cut(rangePart, "-")
		var err error
		if from, err = value(a, lo, hi, names); err != nil {
			return 0, err
		}
		to = from
		switch {
		case isRange:
			if to, err = value(b, lo, hi, names); err != nil {
				return 0, err
			}
			if to < from {
				return 0, fmt.Errorf("bad range %q", rangePart)
			}
		case hasStep: // "5/10" is "5-max/10"
			to = hi
		}
	}
	var set uint64
	for v := from; v <= to; v += step {
		set |= 1 << uint(v)
	}
	return set, nil
}

func value(s string, lo, hi int, names map[string]int) (int, error) {
	if n, ok := names[strings.ToLower(s)]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("bad value %q (allowed %d-%d)", s, lo, hi)
	}
	return n, nil
}

func has(set uint64, v int) bool { return set&(1<<uint(v)) != 0 }

// dayMatches applies the day rule of Vixie cron: with both day fields restricted either may match.
func (c *Cron) dayMatches(t time.Time) bool {
	dom, dow := has(c.dom, t.Day()), has(c.dow, int(t.Weekday()))
	if c.domStar || c.dowStar {
		return dom && dow
	}
	return dom || dow
}

// cronHorizon bounds the search of Next: an expression that matches no date in it (31 February) never
// fires.
const cronHorizon = 5 * 366 * 24 * time.Hour

// maxCronSteps bounds the steps of one search. A search crosses at most the horizon's months and days, and
// within a matching day its hours and minutes, far fewer steps than this; reaching it means the clock of the
// zone did something the search does not know, and the search stops with ErrCronNoProgress rather than spin.
const maxCronSteps = 200_000

// ErrCronNoProgress is a search for the next run that did not end within maxCronSteps.
var ErrCronNoProgress = errors.New("the next run of the cron expression cannot be computed in this timezone")

// Next returns the first time strictly after after at which the expression fires on the wall clock of loc,
// or the zero time when it never does (or cannot be computed, see NextChecked).
func (c *Cron) Next(after time.Time, loc *time.Location) time.Time {
	t, _ := c.NextChecked(after, loc)
	return t
}

// NextChecked is Next with the reason it found no time: nil when the expression never fires within the
// horizon, ErrCronNoProgress when the search was cut. The wall clock is followed through daylight saving
// changes: a time that does not exist on the day the clocks go forward is skipped that day, and a time that
// happens twice on the day they go back fires once, the first time. Every step moves strictly forward: a jump
// to the next day or month that the zone's clock turns into a time not after the current one (a local
// midnight that is missing or repeated on a daylight saving day) becomes a step to the next hour instead.
func (c *Cron) NextChecked(after time.Time, loc *time.Location) (time.Time, error) {
	t := after.In(loc)
	t = t.Add(time.Minute - time.Duration(t.Second())*time.Second - time.Duration(t.Nanosecond()))
	limit := t.Add(cronHorizon)
	for range maxCronSteps {
		if !t.Before(limit) {
			return time.Time{}, nil
		}
		var next time.Time
		switch {
		case !has(c.month, int(t.Month())):
			next = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !c.dayMatches(t):
			next = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case !has(c.hour, t.Hour()):
			next = nextHour(t)
		case !has(c.minute, t.Minute()), repeatedWallClock(t):
			next = t.Add(time.Minute)
		default:
			return t, nil
		}
		if !next.After(t) {
			next = nextHour(t)
		}
		t = next.In(loc)
	}
	return time.Time{}, ErrCronNoProgress
}

// nextHour is the start of the next hour after t on t's clock: always strictly after t.
func nextHour(t time.Time) time.Time {
	return t.Add(time.Hour - time.Duration(t.Minute())*time.Minute)
}

// repeatedWallClock reports whether the wall clock of t was already shown earlier, as the clocks went back
// (by 30 minutes or an hour): t is its second occurrence.
func repeatedWallClock(t time.Time) bool {
	for _, d := range []time.Duration{30 * time.Minute, time.Hour} {
		p := t.Add(-d)
		if p.Day() == t.Day() && p.Hour() == t.Hour() && p.Minute() == t.Minute() {
			return true
		}
	}
	return false
}

// timesOfDay returns the minutes of the day the expression fires at on a day it fires, ascending.
func (c *Cron) timesOfDay() []int {
	var out []int
	for h := 0; h < 24; h++ {
		if !has(c.hour, h) {
			continue
		}
		for m := 0; m < 60; m++ {
			if has(c.minute, m) {
				out = append(out, h*60+m)
			}
		}
	}
	return out
}

// minGap is the shortest time between two runs of the expression on the wall clock: between two times of
// one day, or from the last time of a day to the first of the next.
func (c *Cron) minGap() time.Duration {
	times := c.timesOfDay()
	if len(times) == 0 {
		return 24 * time.Hour
	}
	gap := 24*60 - times[len(times)-1] + times[0]
	for i := 1; i < len(times); i++ {
		gap = min(gap, times[i]-times[i-1])
	}
	return time.Duration(gap) * time.Minute
}

// perDay is how many times the expression fires on a day it fires.
func (c *Cron) perDay() int {
	return bits.OnesCount64(c.hour) * bits.OnesCount64(c.minute)
}

// Rule is when a schedule fires: every EveryMinutes minutes from Anchor, or on Cron in Loc.
type Rule struct {
	EveryMinutes int
	Cron         *Cron
	Loc          *time.Location
	Anchor       time.Time
}

// Next returns the first run strictly after t, or the zero time when the rule never fires. An interval is
// anchored: its runs are Anchor + k·interval, however late each was fired, so they never drift.
func (r Rule) Next(t time.Time) time.Time {
	n, _ := r.NextChecked(t)
	return n
}

// NextChecked is Next with ErrCronNoProgress when a cron's search was cut (Cron.NextChecked).
func (r Rule) NextChecked(t time.Time) (time.Time, error) {
	if r.Cron != nil {
		return r.Cron.NextChecked(t, r.Loc)
	}
	return r.every(t), nil
}

func (r Rule) every(t time.Time) time.Time {
	step := time.Duration(r.EveryMinutes) * time.Minute
	if step <= 0 {
		return time.Time{}
	}
	if t.Before(r.Anchor) {
		return r.Anchor
	}
	k := t.Sub(r.Anchor)/step + 1
	return r.Anchor.Add(k * step)
}

// MinGap is the shortest time between two runs of the rule.
func (r Rule) MinGap() time.Duration {
	if r.Cron != nil {
		return r.Cron.minGap()
	}
	return time.Duration(r.EveryMinutes) * time.Minute
}

// window is how long a run due at due may wait for the principal (HB-06): the shorter of the rule's period
// there and MaxCatchUp.
func (r Rule) window(due time.Time) time.Duration {
	w := MaxCatchUp
	if next := r.Next(due); !next.IsZero() {
		w = min(w, next.Sub(due))
	}
	return max(w, time.Minute)
}

// runsIn returns the runs of the rule after from and up to to, at most limit of them.
func (r Rule) runsIn(from, to time.Time, limit int) []time.Time {
	var out []time.Time
	for t := r.Next(from); !t.IsZero() && !t.After(to) && len(out) < limit; t = r.Next(t) {
		out = append(out, t)
	}
	return out
}

// maxIn24h is the most runs of the given times that fall in any 24 hours: the times are sorted first.
func maxIn24h(times []time.Time) int {
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	best, lo := 0, 0
	for hi := range times {
		for times[hi].Sub(times[lo]) >= 24*time.Hour {
			lo++
		}
		best = max(best, hi-lo+1)
	}
	return best
}
