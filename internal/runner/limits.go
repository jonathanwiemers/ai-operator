package runner

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
)

// UsageLimitError means an engine refused work because of a usage or rate
// limit (subscription window, API rate limit, overload). The run is deferred
// instead of failed.
type UsageLimitError struct {
	Engine aiv1.Engine
	// ResetAt is when the engine said the limit resets; zero if unknown.
	ResetAt time.Time
	Message string
}

func (e *UsageLimitError) Error() string {
	if e.ResetAt.IsZero() {
		return fmt.Sprintf("%s usage limit: %s", e.Engine, e.Message)
	}
	return fmt.Sprintf("%s usage limit until %s: %s", e.Engine, e.ResetAt.UTC().Format(time.RFC3339), e.Message)
}

// AsUsageLimit unwraps a UsageLimitError.
func AsUsageLimit(err error) (*UsageLimitError, bool) {
	var lim *UsageLimitError
	ok := errors.As(err, &lim)
	return lim, ok
}

// limitPatterns match the messages agent CLIs print when a usage or rate
// limit is hit, e.g. "Claude AI usage limit reached|1760000000",
// "5-hour limit reached", "rate_limit_error", "429 Too Many Requests",
// "overloaded_error", "You've hit your usage limit".
// Bare status codes are deliberately not matched: API limit errors carry
// rate_limit_error / overloaded_error anyway, and a stray "429" elsewhere
// must not turn a real failure into an endless deferral.
var limitPatterns = regexp.MustCompile(`(?i)(usage limit|limit reached|rate[ _-]?limit|too many requests|overloaded|quota exceeded|exceeded your .*quota)`)

// MaxDeferrals bounds how often one run request is deferred before it is
// reported as failed, in case an error is mistaken for a usage limit.
const MaxDeferrals = 20

// resetEpoch matches the reset time Claude Code appends as "|<unix seconds>".
var resetEpoch = regexp.MustCompile(`\|(\d{10})\b`)

// classifyLimit turns an engine error into a UsageLimitError if its message
// looks like a usage or rate limit. Only failed invocations are classified,
// so a model that merely talks about rate limits in a successful answer is
// not mistaken for one.
func classifyLimit(engine aiv1.Engine, err error, res *Result) error {
	if err == nil {
		return nil
	}
	if _, ok := AsUsageLimit(err); ok {
		return err
	}
	text := err.Error()
	if res != nil && res.Text != "" {
		text += "\n" + res.Text
	}
	match := limitPatterns.FindString(text)
	if match == "" {
		return err
	}
	lim := &UsageLimitError{Engine: engine, Message: strings.TrimSpace(tail(text, 300))}
	if m := resetEpoch.FindStringSubmatch(text); m != nil {
		if sec, perr := strconv.ParseInt(m[1], 10, 64); perr == nil {
			lim.ResetAt = time.Unix(sec, 0)
		}
	}
	return lim
}
