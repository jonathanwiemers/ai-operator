package runner

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1 "github.com/jonathanwiemers/ai-operator/api/v1alpha1"
	"github.com/jonathanwiemers/ai-operator/internal/research/blocks"
	"github.com/jonathanwiemers/ai-operator/internal/research/store"
)

const (
	// researchLimitBackoff is how long a usage limit without a reset time
	// holds.
	researchLimitBackoff = time.Hour
	// researchReportTimeout bounds recording the outcome after the run's
	// deadline; the pod lives two minutes longer than the run.
	researchReportTimeout = time.Minute
	maxResearchMessage    = 1 << 10
)

// researchReadyInterval is how often the runner checks whether the agent
// container is up.
var researchReadyInterval = 2 * time.Second

// ResearchRun is the runner of one research run (ai-runner research). It
// takes the agent from the AgentResearch's status.run, asks it and records
// the outcome in the store and in status.run; the operator then sets
// finishedAt and deletes the pod.
type ResearchRun struct {
	K8s             client.Client
	Namespace, Name string
	RunID           string
	Agent           Agent
	// OpenStore connects to the research database as the project's writer.
	OpenStore func(context.Context) (*store.Store, error)
	Log       logr.Logger
	now       func() time.Time
}

// Run carries out the run. Outcomes, failures included, go into the store
// and the status; Run returns an error only when it could not report one,
// and the operator then fails the run. A pod whose run is no longer the
// research's current one (stale) exits without writing.
func (rr *ResearchRun) Run(ctx context.Context) error {
	var res aiv1.AgentResearch
	if err := rr.K8s.Get(ctx, types.NamespacedName{Namespace: rr.Namespace, Name: rr.Name}, &res); err != nil {
		return fmt.Errorf("reading the research: %w", err)
	}
	run := res.Status.Run
	if run == nil || run.ID != rr.RunID || run.Outcome != "" || run.FinishedAt != nil {
		rr.Log.Info("the run is not the research's current one, nothing to do", "run", rr.RunID)
		return nil
	}
	started := rr.clock()
	if run.StartedAt != nil {
		started = run.StartedAt.Time
	}
	runCtx, cancel := context.WithTimeout(ctx, res.Spec.Timeout())
	defer cancel()

	var ans ResearchAnswer
	db, err := rr.OpenStore(runCtx)
	if err == nil {
		err = db.StartRun(runCtx, store.Run{ID: run.ID, Namespace: res.Namespace, Research: res.Name,
			StartedAt: started, Engine: string(run.Engine), Model: run.Model, Effort: run.Effort,
			ChoiceReason: run.Reason, ExpiresAt: started.Add(res.Spec.RetentionOrDefault())})
	}
	if err != nil {
		err = fmt.Errorf("starting the run in the research database: %w", err)
	} else {
		ans, err = rr.research(runCtx, db, &res, run)
	}

	report := aiv1.ResearchRunStatus{CostUSD: strconv.FormatFloat(ans.CostUSD, 'f', -1, 64)}
	if ans.Usage != nil {
		reading := *ans.Usage
		reading.Engine, reading.ObservedAt = run.Engine, metav1.Time{Time: rr.clock()}
		report.EngineUsage = aiv1.MergeEngineUsage(run.EngineUsage, reading)
	}
	// The store keeps an error for failed and limited runs only; a degraded
	// run's reason is in its warnings.
	var status, message, storeErr string
	switch lim, limited := AsUsageLimit(err); {
	case limited:
		until := lim.ResetAt
		if !until.After(rr.clock()) {
			until = rr.clock().Add(researchLimitBackoff)
		}
		status, message = store.StatusLimited, lim.Error()
		storeErr = message
		report.UsageLimits = mergeLimit(run.UsageLimits,
			aiv1.UsageLimit{Engine: lim.Engine, Until: metav1.Time{Time: until}, Message: truncate(lim.Message, 300)})
	case err != nil:
		status, message = store.StatusFailed, err.Error()
		storeErr = message
	default:
		status = ans.Status
		if status == store.StatusDegraded && len(ans.Warnings) > 0 {
			message = ans.Warnings[len(ans.Warnings)-1]
		}
	}
	message, storeErr = truncate(message, maxResearchMessage), truncate(storeErr, maxResearchMessage)

	// Recording gets its own time after the run's deadline.
	rctx, rcancel := context.WithTimeout(context.WithoutCancel(ctx), researchReportTimeout)
	defer rcancel()
	// A run that never got into the store is left to the operator, which
	// inserts it from the status.
	if db != nil {
		if ferr := db.FinishRun(rctx, run.ID, status, ans.CostUSD, storeErr, ans.Warnings, rr.clock()); ferr != nil {
			rr.Log.Error(ferr, "recording the outcome in the research database", "run", run.ID)
		}
	}
	report.Outcome, report.Message = aiv1.ResearchOutcome(status), message
	if err := rr.patchRun(rctx, report); err != nil {
		return fmt.Errorf("recording the outcome in the research's status: %w", err)
	}
	rr.Log.Info("research run done", "run", run.ID, "outcome", status, "costUSD", ans.CostUSD, "message", message)
	return nil
}

// research waits for the agent container, asks the agent and saves the
// result.
func (rr *ResearchRun) research(ctx context.Context, db *store.Store, res *aiv1.AgentResearch, run *aiv1.ResearchRunStatus) (ResearchAnswer, error) {
	for {
		err := rr.Agent.Ready(ctx)
		if err == nil {
			break
		}
		rr.Log.Info("waiting for the agent container", "error", err.Error())
		select {
		case <-ctx.Done():
			return ResearchAnswer{}, fmt.Errorf("the agent container did not get ready: %w", err)
		case <-time.After(researchReadyInterval):
		}
	}
	req := ResearchRequest{Prompt: res.Spec.Prompt, Topics: res.Spec.Topics, View: res.Spec.View, Depth: res.Spec.Depth,
		Engine: run.Engine, Model: run.Model, Effort: run.Effort, Now: rr.clock()}
	if req.View == aiv1.ResearchViewNewsflash {
		urls, err := db.PreviousItemURLs(ctx, res.Namespace, res.Name, blocks.MaxItems)
		if err != nil {
			return ResearchAnswer{}, fmt.Errorf("reading the previous run's items: %w", err)
		}
		req.PreviousURLs = urls
	}
	ans, err := RunResearch(logr.NewContext(ctx, rr.Log), rr.Agent, req)
	if err != nil {
		return ans, err
	}
	if err := db.SaveResult(ctx, run.ID, ans.Result, rr.clock()); err != nil {
		return ans, fmt.Errorf("saving the result: %w", err)
	}
	return ans, nil
}

// patchRun merge-patches the runner's report into status.run, so the
// operator's fields stay as they are.
func (rr *ResearchRun) patchRun(ctx context.Context, report aiv1.ResearchRunStatus) error {
	var res aiv1.AgentResearch
	if err := rr.K8s.Get(ctx, types.NamespacedName{Namespace: rr.Namespace, Name: rr.Name}, &res); err != nil {
		return err
	}
	if res.Status.Run == nil || res.Status.Run.ID != rr.RunID {
		return fmt.Errorf("run %s is no longer the research's current run", rr.RunID)
	}
	orig := res.DeepCopy()
	run := res.Status.Run
	run.Outcome, run.Message, run.CostUSD = report.Outcome, report.Message, report.CostUSD
	if report.EngineUsage != nil {
		run.EngineUsage = report.EngineUsage
	}
	if report.UsageLimits != nil {
		run.UsageLimits = report.UsageLimits
	}
	return rr.K8s.Status().Patch(ctx, &res, client.MergeFrom(orig))
}

func (rr *ResearchRun) clock() time.Time {
	if rr.now != nil {
		return rr.now()
	}
	return time.Now()
}
