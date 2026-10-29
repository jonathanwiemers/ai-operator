// Package research holds helpers shared by the AgentResearch controller and
// validation. This file is the cron + timeZone schedule (no Kubernetes imports).
package research

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule is a standard cron expression evaluated in a fixed IANA location.
type Schedule struct {
	sched cron.Schedule
	loc   *time.Location
}

// ParseSchedule parses a standard 5-field cron (or a descriptor such as
// @daily) and an IANA time zone. An empty timeZone means UTC. A CRON_TZ= or
// TZ= prefix inside the schedule is refused; use timeZone instead, as
// Kubernetes CronJobs do for their dedicated field.
func ParseSchedule(spec, timeZone string) (Schedule, error) {
	spec = strings.TrimSpace(spec)
	upper := strings.ToUpper(spec)
	if strings.HasPrefix(upper, "CRON_TZ=") || strings.HasPrefix(upper, "TZ=") {
		return Schedule{}, fmt.Errorf("spec.schedule: CRON_TZ=/TZ= prefix is not allowed; use spec.timeZone")
	}

	name := timeZone
	if name == "" {
		name = "UTC"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return Schedule{}, fmt.Errorf("spec.timeZone: unknown time zone %q", timeZone)
	}

	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return Schedule{}, fmt.Errorf("spec.schedule: %v", err)
	}
	return Schedule{sched: sched, loc: loc}, nil
}

// Next returns the next activation strictly after after, computed in the
// schedule's location and expressed in UTC.
func (s Schedule) Next(after time.Time) time.Time {
	return s.sched.Next(after.In(s.loc)).UTC()
}

// NextRun reports whether a catch-up run is due and the next scheduled time
// after now (UTC). due is true when at least one activation falls after
// lastScheduled and at or before now; at most one catch-up is signaled no
// matter how many were missed. next is always the first activation after now.
func NextRun(s Schedule, lastScheduled, now time.Time) (due bool, next time.Time) {
	next = s.Next(now)
	missed := s.Next(lastScheduled)
	if !missed.After(now) {
		due = true
	}
	return due, next
}
