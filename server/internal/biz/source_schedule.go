package biz

import (
	"fmt"
	"strings"
	"time"

	// The timezone database is embedded so schedule evaluation never depends on
	// the host image shipping zoneinfo.
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

// DefaultScheduleTimezone is used when a plan does not name a zone.
const DefaultScheduleTimezone = "Asia/Shanghai"

// scheduleParser accepts standard 5-field cron expressions plus @descriptors.
var scheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ValidateSchedule checks a cron expression and its timezone.
func ValidateSchedule(expression, timezone string) error {
	_, _, err := resolveSchedule(expression, timezone)
	return err
}

// resolveSchedule parses the expression and returns the schedule and zone.
func resolveSchedule(expression, timezone string) (cron.Schedule, *time.Location, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, nil, fmt.Errorf("%w: cron expression is empty", ErrPlanSchedule)
	}
	schedule, err := scheduleParser.Parse(expression)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrPlanSchedule, err)
	}
	location, err := time.LoadLocation(NormalizeScheduleTimezone(timezone))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: timezone %q: %v", ErrPlanSchedule, timezone, err)
	}
	return schedule, location, nil
}

// NormalizeScheduleTimezone falls back to the default zone for empty input.
func NormalizeScheduleTimezone(timezone string) string {
	trimmed := strings.TrimSpace(timezone)
	if trimmed == "" {
		return DefaultScheduleTimezone
	}
	return trimmed
}

// NextRunAfter returns the first activation strictly after from, stored in UTC.
func NextRunAfter(expression, timezone string, from time.Time) (time.Time, error) {
	schedule, location, err := resolveSchedule(expression, timezone)
	if err != nil {
		return time.Time{}, err
	}
	next := schedule.Next(from.In(location))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("%w: cron %q has no future activation", ErrPlanSchedule, expression)
	}
	return next.UTC(), nil
}

// normalizeSchedule validates the schedule fields of a plan and computes the
// next fire time. An empty expression keeps the plan manual.
func normalizeSchedule(expression, timezone string, from time.Time) (string, string, *time.Time, error) {
	expression = strings.TrimSpace(expression)
	timezone = NormalizeScheduleTimezone(timezone)
	if expression == "" {
		return "", timezone, nil, nil
	}
	next, err := NextRunAfter(expression, timezone, from)
	if err != nil {
		return "", "", nil, err
	}
	return expression, timezone, &next, nil
}
