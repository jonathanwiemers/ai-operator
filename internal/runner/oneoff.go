package runner

import (
	"context"
	"regexp"
	"time"

	"github.com/go-logr/logr"

	"github.com/jonathanwiemers/ai-operator/internal/gitlab"
)

// oneOffWaits are the waits between the attempts of retryOneOff; a
// variable, so tests can set them to zero.
var oneOffWaits = []time.Duration{10 * time.Second, 30 * time.Second}

// retryOneOff calls fn up to len(oneOffWaits)+1 times while it fails with
// an error transient reports as one-off (a timeout, a reset connection, a
// 5xx). Any other error, or a done ctx, ends it at once.
func retryOneOff(ctx context.Context, log logr.Logger, what string, transient func(error) bool, fn func() error) error {
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil || attempt > len(oneOffWaits) || !transient(err) {
			return err
		}
		log.Info("one-off error, trying again", "what", what, "attempt", attempt, "wait", oneOffWaits[attempt-1].String(), "error", err.Error())
		select {
		case <-ctx.Done():
			return err
		case <-time.After(oneOffWaits[attempt-1]):
		}
	}
}

// gitOneOff matches what git and curl print for network errors that a
// later attempt may not get.
var gitOneOff = regexp.MustCompile(`(?i)timed out|curl 28|connection reset|could not resolve host|RPC failed; HTTP (5\d\d|429)|returned error: (5\d\d|429)|error: 429|HTTP 429|early EOF|unexpected disconnect`)

// gitTransient reports whether a git command failed with a one-off network
// error. A rejected push, a 403 or the push guard's refusal is not one.
func gitTransient(err error) bool {
	return err != nil && (gitOneOff.MatchString(err.Error()) || gitlab.IsTransient(err))
}
