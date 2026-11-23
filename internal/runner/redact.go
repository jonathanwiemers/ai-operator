package runner

import (
	"errors"

	"github.com/go-logr/logr"
	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// RedactLogger returns log with secrets and known tokens redacted
// (textutil.Redact) from every message, string value and error it writes.
// The runner and the agent server log what the agent says and runs (logEvent)
// and errors that quote CLIs and git; these reach the pod logs, which the
// Desk shows. An agent that ran `env` must not put a key there.
func RedactLogger(log logr.Logger, secrets []string) logr.Logger {
	sink := log.GetSink()
	if sink == nil {
		return log
	}
	if cd, ok := sink.(logr.CallDepthLogSink); ok {
		sink = cd.WithCallDepth(1) // the caller, not redactSink
	}
	return log.WithSink(redactSink{sink, secrets})
}

type redactSink struct {
	logr.LogSink
	secrets []string
}

func (s redactSink) redact(kv []any) []any {
	out := make([]any, len(kv))
	for i, v := range kv {
		switch v := v.(type) {
		case string:
			out[i] = textutil.Redact(v, s.secrets...)
		case error:
			out[i] = textutil.Redact(v.Error(), s.secrets...)
		default:
			out[i] = v
		}
	}
	return out
}

func (s redactSink) Info(level int, msg string, kv ...any) {
	s.LogSink.Info(level, textutil.Redact(msg, s.secrets...), s.redact(kv)...)
}

func (s redactSink) Error(err error, msg string, kv ...any) {
	if err != nil {
		err = errors.New(textutil.Redact(err.Error(), s.secrets...))
	}
	s.LogSink.Error(err, textutil.Redact(msg, s.secrets...), s.redact(kv)...)
}

func (s redactSink) WithValues(kv ...any) logr.LogSink {
	return redactSink{s.LogSink.WithValues(s.redact(kv)...), s.secrets}
}

func (s redactSink) WithName(name string) logr.LogSink {
	return redactSink{s.LogSink.WithName(name), s.secrets}
}

func (s redactSink) WithCallDepth(depth int) logr.LogSink {
	if cd, ok := s.LogSink.(logr.CallDepthLogSink); ok {
		return redactSink{cd.WithCallDepth(depth), s.secrets}
	}
	return s
}
