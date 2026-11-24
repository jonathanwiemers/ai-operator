// Package timefmt writes clock times in server text. Every server message
// that names a clock time (reasons, status messages, GitLab comments, agent
// prompts) uses UTCMinute, so that the Desk can find each time in one form,
// "2006-01-02 15:04 UTC", and show it in the browser's time zone. The scan
// test keeps other clock layouts out of internal/ and cmd/.
package timefmt

import "time"

// UTCMinute is t in UTC to the minute, with the date and the zone, e.g.
// "2026-11-21 14:05 UTC".
func UTCMinute(t time.Time) string { return t.UTC().Format("2006-01-02 15:04 UTC") }
