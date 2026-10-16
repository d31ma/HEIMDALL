// Package cron parses the five-field POSIX cron expression compose files
// carry, and answers the only question a scheduler asks of one: when does
// this next fire, in this timezone?
//
// It deliberately models the classic five fields and nothing else. Seconds,
// years, @reboot, and Quartz's ? are refused rather than translated, because
// a six-field expression means different things on different runtimes and
// guessing which is how a nightly job runs every minute.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Expression is a parsed cron expression.
type Expression struct {
	minutes    map[int]bool
	hours      map[int]bool
	daysOfWeek map[int]bool
	daysOfMont map[int]bool
	months     map[int]bool
	// restricted records which of the two day fields were written, because
	// cron's day rule is a union when both are set and an intersection when
	// only one is.
	dayOfMonthSet bool
	dayOfWeekSet  bool
	text          string
}

func (e Expression) String() string { return e.text }

var names = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

// Parse validates and compiles one expression.
func Parse(expression string) (Expression, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return Expression{}, fmt.Errorf(
			"%q has %d fields; a cron expression is five: minute hour day-of-month month day-of-week",
			expression, len(fields))
	}

	parsed := Expression{text: strings.Join(fields, " ")}
	var err error
	if parsed.minutes, err = field(fields[0], 0, 59); err != nil {
		return Expression{}, fmt.Errorf("%q: minute: %w", expression, err)
	}
	if parsed.hours, err = field(fields[1], 0, 23); err != nil {
		return Expression{}, fmt.Errorf("%q: hour: %w", expression, err)
	}
	if parsed.daysOfMont, err = field(fields[2], 1, 31); err != nil {
		return Expression{}, fmt.Errorf("%q: day of month: %w", expression, err)
	}
	if parsed.months, err = field(fields[3], 1, 12); err != nil {
		return Expression{}, fmt.Errorf("%q: month: %w", expression, err)
	}
	if parsed.daysOfWeek, err = field(fields[4], 0, 7); err != nil {
		return Expression{}, fmt.Errorf("%q: day of week: %w", expression, err)
	}
	// 7 and 0 are both Sunday.
	if parsed.daysOfWeek[7] {
		parsed.daysOfWeek[0] = true
	}
	parsed.dayOfMonthSet = fields[2] != "*"
	parsed.dayOfWeekSet = fields[4] != "*"
	return parsed, nil
}

// field compiles one field into the set of values it matches.
func field(text string, low, high int) (map[int]bool, error) {
	if text == "" {
		return nil, fmt.Errorf("is empty")
	}
	out := map[int]bool{}
	for _, alternative := range strings.Split(text, ",") {
		body, stepText, hasStep := strings.Cut(alternative, "/")
		step := 1
		if hasStep {
			parsed, err := strconv.Atoi(stepText)
			if err != nil || parsed <= 0 {
				return nil, fmt.Errorf("step %q is not a positive number", stepText)
			}
			step = parsed
		}

		start, end := low, high
		if body != "*" {
			bounds := strings.SplitN(body, "-", 2)
			value, err := number(bounds[0], low, high)
			if err != nil {
				return nil, err
			}
			start, end = value, value
			if len(bounds) == 2 {
				if end, err = number(bounds[1], low, high); err != nil {
					return nil, err
				}
			} else if hasStep {
				// "5/10" means from 5 to the end of the range, as cron reads it.
				end = high
			}
		}
		if end < start {
			return nil, fmt.Errorf("range %q ends before it starts", body)
		}
		for value := start; value <= end; value += step {
			out[value] = true
		}
	}
	return out, nil
}

func number(text string, low, high int) (int, error) {
	if value, named := names[strings.ToUpper(text)]; named {
		if value < low || value > high {
			return 0, fmt.Errorf("%q is not valid in this field", text)
		}
		return value, nil
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number, a name, or *", text)
	}
	if value < low || value > high {
		return 0, fmt.Errorf("%d is outside %d-%d", value, low, high)
	}
	return value, nil
}

// maxSearch bounds the walk forward. A schedule that matches nothing within a
// year — 31 February — must return "never" rather than spin.
const maxSearch = 366 * 24 * 60

// Next is the first firing strictly after `after`, in `location`. The zero
// time means the expression never fires again, which only a calendar
// impossibility produces.
func (e Expression) Next(after time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.UTC
	}
	candidate := after.In(location).Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < maxSearch; i++ {
		if e.matches(candidate) {
			return candidate
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}
}

func (e Expression) matches(when time.Time) bool {
	if !e.minutes[when.Minute()] || !e.hours[when.Hour()] || !e.months[int(when.Month())] {
		return false
	}
	dayOfMonth := e.daysOfMont[when.Day()]
	dayOfWeek := e.daysOfWeek[int(when.Weekday())]
	switch {
	case e.dayOfMonthSet && e.dayOfWeekSet:
		// Classic cron: when both are restricted, either matching fires.
		return dayOfMonth || dayOfWeek
	case e.dayOfMonthSet:
		return dayOfMonth
	case e.dayOfWeekSet:
		return dayOfWeek
	default:
		return true
	}
}
