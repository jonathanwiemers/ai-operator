package v1alpha1

import (
	"fmt"
	"hash/fnv"
	"strconv"
)

// ConditionExperimentValid is the AgentProject condition that tells whether
// agents.implement.experiment is valid: True, or False, reason
// InvalidExperiment, with the problems in the message. Absent without an
// experiment.
const ConditionExperimentValid = "ExperimentValid"

// ExperimentErrors says what is wrong with agents.implement.experiment
// beyond what the CRD validates: an arm effort above agents.maxEffort, an
// arm profile the project does not have, more than one arm without
// overrides. Nil without an experiment.
func (s AgentProjectSpec) ExperimentErrors() []string {
	e := s.Agents.Implement.Experiment
	if e == nil {
		return nil
	}
	d := s.WithDefaults()
	var errs []string
	empty := 0
	for _, a := range e.Arms {
		if a.Effort != "" {
			if _, lowered := d.CapEffort(a.Effort); lowered {
				errs = append(errs, fmt.Sprintf("arm %s: the effort %s is above agents.maxEffort %s", a.Name, a.Effort, d.Agents.MaxEffort))
			}
		}
		if a.Profile != "" {
			if _, ok := d.Profile(a.Profile); !ok {
				errs = append(errs, fmt.Sprintf("arm %s: the project has no profile %s", a.Name, a.Profile))
			}
		}
		if a.Empty() {
			empty++
		}
	}
	if empty > 1 {
		errs = append(errs, fmt.Sprintf("%d arms override nothing; at most one may", empty))
	}
	return errs
}

// ValidExperiment returns agents.implement.experiment if it is set and
// valid.
func (s AgentProjectSpec) ValidExperiment() (Experiment, bool) {
	e := s.Agents.Implement.Experiment
	if e == nil || len(e.Arms) == 0 || len(s.ExperimentErrors()) > 0 {
		return Experiment{}, false
	}
	return *e, true
}

// Arm returns the experiment's arm with the given name.
func (e Experiment) Arm(name string) (ExperimentArm, bool) {
	for _, a := range e.Arms {
		if a.Name == name {
			return a, true
		}
	}
	return ExperimentArm{}, false
}

// ArmFor picks the arm of an issue: FNV-1a 64 over the normalized project
// path, the experiment's name and the issue's IID, mixed, modulo the arm
// count. The same inputs always give the same arm. e must have arms.
func ArmFor(project string, e Experiment, iid int64) ExperimentArm {
	h := fnv.New64a()
	h.Write([]byte(NormalizeProject(project) + "\n" + e.Name + "\n" + strconv.FormatInt(iid, 10))) // #nosec G104 -- a hash.Hash's Write never returns an error (package hash doc)
	return e.Arms[mix64(h.Sum64())%uint64(len(e.Arms))]
}

// mix64 is murmur3's 64-bit finalizer. FNV-1a's low bits depend only on
// the low bits of the input bytes, so without it a power-of-two arm count
// would pick arms by the IID's last digit, and another experiment name
// would only permute the arms instead of reshuffling the issues.
func mix64(h uint64) uint64 {
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

// EstimatedTokens estimates the tokens of a tool result from its size:
// bytes / 4.
func EstimatedTokens(bytes int64) int64 { return bytes / 4 }
