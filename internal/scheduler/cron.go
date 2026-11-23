package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronSpec struct {
	minute        uint64 // bits 0..59
	hour          uint32 // bits 0..23
	dom           uint32 // bits 1..31
	month         uint16 // bits 1..12
	dow           uint8  // bits 0..6 (0=Sun)
	domRestricted bool
	dowRestricted bool
}

var cronMacros = map[string]string{
	"@hourly":   "0 * * * *",
	"@daily":    "0 9 * * *",
	"@midnight": "0 0 * * *",
	"@weekly":   "0 9 * * 1",
	"@monthly":  "0 9 1 * *",
}

// ValidateTrigger validates a cron or interval trigger specification.
func ValidateTrigger(spec TriggerSpec) error {
	if _, err := resolveLocation(spec.Timezone); err != nil {
		return err
	}
	switch spec.Kind {
	case TriggerKindCron:
		if strings.TrimSpace(spec.Cron) == "" {
			return errors.New("cron expression is required")
		}
		_, err := parseCron(spec.Cron)
		return err
	case TriggerKindInterval:
		if spec.IntervalMinutes < MinIntervalMinutes || spec.IntervalMinutes > MaxIntervalMinutes {
			return fmt.Errorf("intervalMinutes must be between %d and %d", MinIntervalMinutes, MaxIntervalMinutes)
		}
		return nil
	default:
		return fmt.Errorf("unsupported trigger kind %q (expected cron or interval)", spec.Kind)
	}
}

// PreviewNextRuns returns the next count scheduled timestamps strictly after now.
func PreviewNextRuns(spec TriggerSpec, now time.Time, count int) PreviewResponse {
	if count <= 0 {
		count = 5
	}
	if count > 20 {
		count = 20
	}
	if err := ValidateTrigger(spec); err != nil {
		return PreviewResponse{Valid: false, Error: err.Error()}
	}
	loc, _ := resolveLocation(spec.Timezone)
	out := make([]int64, 0, count)
	cursor := now
	for i := 0; i < count; i++ {
		next, err := NextFireTime(spec, cursor)
		if err != nil {
			return PreviewResponse{Valid: false, Error: err.Error()}
		}
		out = append(out, next.UnixMilli())
		cursor = next
	}
	return PreviewResponse{
		Valid:    true,
		Timezone: loc.String(),
		NextRuns: out,
	}
}

// NextFireTime computes the earliest fire time strictly after after.
func NextFireTime(spec TriggerSpec, after time.Time) (time.Time, error) {
	loc, err := resolveLocation(spec.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	switch spec.Kind {
	case TriggerKindInterval:
		if spec.IntervalMinutes < MinIntervalMinutes || spec.IntervalMinutes > MaxIntervalMinutes {
			return time.Time{}, fmt.Errorf("intervalMinutes must be between %d and %d", MinIntervalMinutes, MaxIntervalMinutes)
		}
		step := time.Duration(spec.IntervalMinutes) * time.Minute
		return after.In(loc).Truncate(time.Second).Add(step), nil
	case TriggerKindCron:
		parsed, err := parseCron(spec.Cron)
		if err != nil {
			return time.Time{}, err
		}
		return parsed.next(after.In(loc))
	default:
		return time.Time{}, fmt.Errorf("unsupported trigger kind %q", spec.Kind)
	}
}

func resolveLocation(tz string) (*time.Location, error) {
	trimmed := strings.TrimSpace(tz)
	if trimmed == "" || strings.EqualFold(trimmed, "local") {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", trimmed, err)
	}
	return loc, nil
}

func parseCron(expr string) (cronSpec, error) {
	raw := strings.TrimSpace(expr)
	if macro, ok := cronMacros[strings.ToLower(raw)]; ok {
		raw = macro
	}
	fields := strings.Fields(raw)
	if len(fields) != 5 {
		return cronSpec{}, fmt.Errorf("cron expression must have 5 fields (minute hour dom month dow), got %d", len(fields))
	}
	minMask, _, err := parseCronField(fields[0], 0, 59, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("minute field: %w", err)
	}
	hourMask, _, err := parseCronField(fields[1], 0, 23, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("hour field: %w", err)
	}
	domMask, domRestr, err := parseCronField(fields[2], 1, 31, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("day-of-month field: %w", err)
	}
	monthMask, _, err := parseCronField(fields[3], 1, 12, false)
	if err != nil {
		return cronSpec{}, fmt.Errorf("month field: %w", err)
	}
	dowMask, dowRestr, err := parseCronField(fields[4], 0, 7, true)
	if err != nil {
		return cronSpec{}, fmt.Errorf("day-of-week field: %w", err)
	}
	// Map 7 (Sunday alias) to 0.
	if dowMask&(1<<7) != 0 {
		dowMask |= 1 << 0
		dowMask &^= 1 << 7
	}
	spec := cronSpec{
		minute:        minMask,
		hour:          uint32(hourMask),
		dom:           uint32(domMask),
		month:         uint16(monthMask),
		dow:           uint8(dowMask),
		domRestricted: domRestr,
		dowRestricted: dowRestr,
	}
	// Verify at least one reachable date exists within 4 years (catches e.g. Feb 31).
	if _, err := spec.next(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		return cronSpec{}, err
	}
	return spec, nil
}

func parseCronField(field string, minVal, maxVal int, allowDOWNames bool) (uint64, bool, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		return 0, false, errors.New("empty field")
	}
	restricted := field != "*" && field != "?"
	var mask uint64
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, false, errors.New("empty list item")
		}
		rangePart, stepPart, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(strings.TrimSpace(stepPart))
			if err != nil || n <= 0 {
				return 0, false, fmt.Errorf("invalid step %q", stepPart)
			}
			step = n
		}
		var lo, hi int
		switch {
		case rangePart == "*" || rangePart == "?":
			lo, hi = minVal, maxVal
			if allowDOWNames && maxVal == 7 {
				hi = 6
			}
		case strings.Contains(rangePart, "-"):
			aStr, bStr, _ := strings.Cut(rangePart, "-")
			a, err := parseCronAtom(aStr, allowDOWNames)
			if err != nil {
				return 0, false, err
			}
			b, err := parseCronAtom(bStr, allowDOWNames)
			if err != nil {
				return 0, false, err
			}
			if a < minVal || b > maxVal || a > b {
				return 0, false, fmt.Errorf("range %d-%d out of bounds [%d..%d]", a, b, minVal, maxVal)
			}
			lo, hi = a, b
		default:
			v, err := parseCronAtom(rangePart, allowDOWNames)
			if err != nil {
				return 0, false, err
			}
			if v < minVal || v > maxVal {
				return 0, false, fmt.Errorf("value %d out of bounds [%d..%d]", v, minVal, maxVal)
			}
			lo = v
			if hasStep {
				hi = maxVal
				if allowDOWNames && maxVal == 7 {
					hi = 6
				}
			} else {
				hi = v
			}
		}
		for v := lo; v <= hi; v += step {
			mask |= 1 << uint(v)
		}
	}
	if mask == 0 {
		return 0, false, errors.New("matches no values")
	}
	return mask, restricted, nil
}

var dowNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

func parseCronAtom(raw string, allowDOWNames bool) (int, error) {
	s := strings.TrimSpace(strings.ToLower(raw))
	if allowDOWNames {
		if v, ok := dowNames[s]; ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid token %q", raw)
	}
	return v, nil
}

func (c cronSpec) matchesDay(t time.Time) bool {
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domRestricted && c.dowRestricted {
		return domOK || dowOK
	}
	return domOK && dowOK
}

func (c cronSpec) next(after time.Time) (time.Time, error) {
	// Advance to the start of the next whole minute strictly after `after`.
	t := after.Truncate(time.Minute).Add(time.Minute)
	limitYear := t.Year() + 5
	for t.Year() <= limitYear {
		if c.month&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
			continue
		}
		if !c.matchesDay(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}
		if c.hour&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			continue
		}
		if c.minute&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t, nil
	}
	return time.Time{}, errors.New("cron expression matches no valid date within 5 years")
}
