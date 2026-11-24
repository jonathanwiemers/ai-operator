package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jonathanwiemers/ai-operator/internal/textutil"
)

// errNoStreamCall is a message for a call that is not running (any more).
var errNoStreamCall = errors.New("no stream-input call is running")

// messageWait is how long Message waits for the replay of its text. A
// variable so tests can shorten it.
var messageWait = 60 * time.Second

// MessageRequest is a user message for the running stream-input call. ID
// names it in the logs; the agent server never logs Text.
type MessageRequest struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// MessageResult says whether the call took the message in: Delivered once
// its replay showed up in the call's output, else Reason says why not.
type MessageResult struct {
	Delivered bool   `json:"delivered"`
	Reason    string `json:"reason,omitempty"`
}

// Message writes a user message into the stream-input call in flight and
// waits for its replay, the call's result or messageWait, whichever comes
// first. Without such a call, or when the write fails, it errors at once.
func (a *LocalAgent) Message(ctx context.Context, req MessageRequest) (MessageResult, error) {
	if strings.TrimSpace(req.Text) == "" {
		return MessageResult{}, errors.New("empty message")
	}
	a.liveMu.Lock()
	in := a.live
	a.liveMu.Unlock()
	if in == nil {
		return MessageResult{}, errNoStreamCall
	}
	return in.message(ctx, req.Text, messageWait)
}

// setLive fills or (nil) empties the slot of the stream-input call in
// flight; implementer calls are sequential, so one slot does.
func (a *LocalAgent) setLive(in *liveInput) {
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	a.live = in
}

// liveInputKey holds the function runCLI hands a stream-input call's
// liveInput to once the prompt is written (LocalAgent.setLive).
type liveInputKey struct{}

// liveInput is the stdin of a running stream-input call (claude
// --input-format stream-json): the prompt goes in as the first user message,
// and stdin stays open for more until the call's first result line.
type liveInput struct {
	w     io.WriteCloser
	mu    sync.Mutex // one line at a time
	msgMu sync.Mutex // one Message waiting for its replay
	once  sync.Once
	// done is closed with stdin: the result line came, or the call ended.
	done chan struct{}
	// replays are the texts of the replayed user messages (not the
	// prompt's), as the call's turnReader sees them.
	replays chan string
}

func newLiveInput(w io.WriteCloser) *liveInput {
	return &liveInput{w: w, done: make(chan struct{}), replays: make(chan string, 8)}
}

// start writes the prompt once the CLI runs, then offers the call to the
// slot in ctx. A failed write is logged: the CLI then ends without a result,
// and runCLI reports that with its stderr.
func (in *liveInput) start(ctx context.Context, tr *turnReader, prompt string) {
	tr.prompt, tr.replayed = strings.TrimSpace(prompt), in.replayed
	if err := in.send(prompt); err != nil {
		tr.log.Info("could not write the prompt to the agent CLI", "error", err.Error())
		return
	}
	if set, ok := ctx.Value(liveInputKey{}).(func(*liveInput)); ok {
		set(in)
	}
}

// send writes text as one user message.
func (in *liveInput) send(text string) error {
	b, err := json.Marshal(claudeUserMessage(text))
	if err != nil {
		return err
	}
	select {
	case <-in.done:
		return errNoStreamCall
	default:
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	_, err = in.w.Write(append(b, '\n'))
	return err
}

// replayed reports a replay; a full buffer drops it (nobody waits for it).
func (in *liveInput) replayed(text string) {
	select {
	case in.replays <- text:
	default:
	}
}

// message writes text and waits for its replay (see LocalAgent.Message).
func (in *liveInput) message(ctx context.Context, text string, wait time.Duration) (MessageResult, error) {
	if !in.msgMu.TryLock() {
		return MessageResult{}, errors.New("another message is waiting for its replay")
	}
	defer in.msgMu.Unlock()
	// Late replays of an earlier message are no answer to this one.
	for drained := false; !drained; {
		select {
		case <-in.replays:
		default:
			drained = true
		}
	}
	if err := in.send(text); err != nil {
		return MessageResult{}, err
	}
	want := strings.TrimSpace(text)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case got := <-in.replays:
			if got == want {
				return MessageResult{Delivered: true}, nil
			}
		case <-in.done:
			// The turnReader reports a replay before it reads the result.
			for {
				select {
				case got := <-in.replays:
					if got == want {
						return MessageResult{Delivered: true}, nil
					}
				default:
					return MessageResult{Reason: "the call answered before the message was replayed"}, nil
				}
			}
		case <-timer.C:
			return MessageResult{Reason: fmt.Sprintf("no replay within %s", wait)}, nil
		case <-ctx.Done():
			return MessageResult{}, ctx.Err()
		}
	}
}

// close closes stdin, so the CLI exits after its answer. It does not wait
// for a write in progress: that one fails.
func (in *liveInput) close() {
	in.once.Do(func() {
		close(in.done)
		_ = in.w.Close()
	})
}

// replayText is the text of a claude user line that replays a message
// written on stdin (--replay-user-messages marks it isReplay), and whether
// the line is one.
func replayText(line []byte) (string, bool) {
	if !bytes.Contains(line, []byte(`"isReplay"`)) {
		return "", false
	}
	var ev struct {
		IsReplay bool `json:"isReplay"`
		Message  struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &ev) != nil || !ev.IsReplay {
		return "", false
	}
	return toolResultText(ev.Message.Content), true
}

// userTurn handles a replayed user message. The prompt's own replay is
// skipped (prompts are logged only with agents.logPrompts); any other is a
// user turn of the transcript and goes to replayed.
func (t *turnReader) userTurn(text string) {
	text = strings.TrimSpace(text)
	if text == "" || text == t.prompt {
		return
	}
	t.log.Info("user says", "text", textutil.Truncate(text, maxLogText))
	if t.replayed != nil {
		t.replayed(text)
	}
}
