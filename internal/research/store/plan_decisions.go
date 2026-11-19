package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// The history of plans' decisions, copied by the operator (the owner) from
// AgentPlan.status.decisions and read by the UI. The status keeps the
// current list; the store keeps every version and every sign-off seen.

// PlanKey names one plan of a project.
type PlanKey struct{ Namespace, Project, Plan string }

// PlanDecision is the content of one version of a plan's decision. State is
// open or locked, DecidedBy "agent" or the owner's OIDC subject.
type PlanDecision struct {
	ID            string `db:"decision_id"`
	Version       int64
	Title         string
	Question      string
	Options       []string
	Recommended   int
	Why           string
	State         string
	DecidedBy     string
	DecidedByName string
	DecidedAt     time.Time
}

// PlanDecisionSignOff is one owner's sign-off of a version: By is the
// owner's OIDC subject.
type PlanDecisionSignOff struct {
	By     string    `db:"signed_off_by"`
	ByName string    `db:"signed_off_by_name"`
	At     time.Time `db:"signed_off_at"`
}

// PlanDecisionRow is one entry of a plan's current decision list, with its
// latest sign-off (nil while the version is not signed off).
type PlanDecisionRow struct {
	PlanDecision
	SignOff *PlanDecisionSignOff
}

// PlanDecisionVersion is a stored version with its sign-offs, newest first.
type PlanDecisionVersion struct {
	PlanDecision
	SignOffs []PlanDecisionSignOff `db:"-"`
}

// SavePlanDecisions stores the versions in list that are new, and the
// sign-offs that are new, in one transaction, with expiresAt. A stored
// version keeps its content and expiry, except a changed why; storing the
// same list again changes nothing.
func (s *Store) SavePlanDecisions(ctx context.Context, key PlanKey, list []PlanDecisionRow, expiresAt time.Time) error {
	b := &pgx.Batch{}
	for _, d := range list {
		b.Queue(`INSERT INTO plan_decision_versions (namespace, project, plan, decision_id, seq, version, title,
				question, options, recommended, why, state, decided_by, decided_by_name, decided_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (namespace, project, plan, decision_id, version) DO UPDATE SET why = EXCLUDED.why
				WHERE plan_decision_versions.why IS DISTINCT FROM EXCLUDED.why`,
			key.Namespace, key.Project, key.Plan, d.ID, planDecisionSeq(d.ID), d.Version, d.Title, d.Question,
			nonNil(d.Options), d.Recommended, d.Why, d.State, d.DecidedBy, d.DecidedByName, d.DecidedAt, expiresAt)
		if o := d.SignOff; o != nil {
			b.Queue(`INSERT INTO plan_decision_signoffs (namespace, project, plan, decision_id, version,
					signed_off_by, signed_off_by_name, signed_off_at, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT DO NOTHING`,
				key.Namespace, key.Project, key.Plan, d.ID, d.Version, o.By, o.ByName, o.At, expiresAt)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return tx.SendBatch(ctx, b).Close()
	})
}

// planDecisionSeq is the number after "D" of a plan decision ID: 12 for D12,
// 0 when there is none.
func planDecisionSeq(id string) int {
	seq, _ := strconv.Atoi(strings.TrimPrefix(id, "D"))
	return seq
}

// PlanDecisionHistory returns the unexpired versions of the plan's
// decisions: by decision number, each decision's newest version first, with
// its unexpired sign-offs newest first.
func (s *Store) PlanDecisionHistory(ctx context.Context, namespace, project, plan string) ([]PlanDecisionVersion, error) {
	rows, _ := s.pool.Query(ctx, `SELECT decision_id, version, title, question, options, recommended, why, state,
			decided_by, decided_by_name, decided_at
		FROM plan_decision_versions
		WHERE namespace = $1 AND project = $2 AND plan = $3 AND expires_at > now()
		ORDER BY seq, decision_id, version DESC`, namespace, project, plan)
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[PlanDecisionVersion])
	if err != nil || len(out) == 0 {
		return out, err
	}
	type versionKey struct {
		id      string
		version int64
	}
	at := make(map[versionKey]int, len(out))
	for i, v := range out {
		at[versionKey{v.ID, v.Version}] = i
	}
	rows, _ = s.pool.Query(ctx, `SELECT decision_id, version, signed_off_by, signed_off_by_name, signed_off_at
		FROM plan_decision_signoffs
		WHERE namespace = $1 AND project = $2 AND plan = $3 AND expires_at > now()
		ORDER BY signed_off_at DESC, signed_off_by`, namespace, project, plan)
	var signOff PlanDecisionSignOff
	var k versionKey
	_, err = pgx.ForEachRow(rows, []any{&k.id, &k.version, &signOff.By, &signOff.ByName, &signOff.At}, func() error {
		if i, ok := at[k]; ok {
			out[i].SignOffs = append(out[i].SignOffs, signOff)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteExpiredPlanDecisions deletes the decision versions and sign-offs
// that expired before now and returns how many rows. It runs as the owner.
func (s *Store) DeleteExpiredPlanDecisions(ctx context.Context, now time.Time) (int64, error) {
	return s.deleteExpiredRows(ctx, now, "plan_decision_versions", "plan_decision_signoffs")
}
