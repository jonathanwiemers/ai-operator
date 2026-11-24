package v1alpha1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/jonathanwiemers/ai-operator/internal/projectglob"
)

// AgentProjectSetSpec makes the operator work in every project of a GitLab
// group that the include globs match and the exclude globs do not: the set
// writes one child AgentProject per active project from its template.
type AgentProjectSetSpec struct {
	// GitLab is the instance, token and root group the set discovers
	// projects in.
	GitLab ProjectSetGitLab `json:"gitlab"`

	// Include are path globs of the projects the set works in, e.g.
	// "my-group/**": `*` matches within one path segment, `**` across
	// segments, case-insensitive.
	// +kubebuilder:validation:MinItems=1
	Include []string `json:"include"`

	// Exclude are path globs of projects the set never works in, whatever
	// Include says, e.g. "my-group/archived/**".
	// +optional
	Exclude []string `json:"exclude,omitempty"`

	// Template is the spec of every child AgentProject: any AgentProject
	// spec field except gitlab, which the set fills in from its own gitlab
	// and the project's path. Unknown fields make the set Ready=False,
	// reason InvalidTemplate. The template needs webhook.enabled,
	// webhook.secretRef and webhook.url (reason WebhookRequired).
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	Template *runtime.RawExtension `json:"template,omitempty"`

	// Projects are projects of the group the set names, each with its own
	// settings merged onto template. Each path lies below gitlab.group and
	// appears once.
	// +listType=map
	// +listMapKey=path
	// +optional
	Projects []ProjectSetEntry `json:"projects,omitempty"`

	// PollInterval is how often the set discovers the group's projects.
	// +kubebuilder:default="2m"
	// +optional
	PollInterval metav1.Duration `json:"pollInterval,omitempty"`

	// DormantAfter is how long a child stays without work (no milestone
	// with the spec marker, no ready issue, no AgentTask) before the set
	// removes it.
	// +kubebuilder:default="168h"
	// +optional
	DormantAfter metav1.Duration `json:"dormantAfter,omitempty"`
}

// ProjectSetEntry is one project the set names, with its own settings.
type ProjectSetEntry struct {
	// Path is the project's full path below gitlab.group, e.g.
	// "my-group/shop". No globs.
	// +kubebuilder:validation:MinLength=1
	Path string `json:"path"`

	// Name is the name of the project's child AgentProject. Empty, the set
	// derives it from the path.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Name string `json:"name,omitempty"`

	// Spec are the project's settings, merged onto template: any
	// AgentProject spec field except gitlab. A null value removes the
	// template's key. Unknown fields make the set Ready=False, reason
	// InvalidTemplate.
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	// +optional
	Spec *runtime.RawExtension `json:"spec,omitempty"`
}

// ProjectSetGitLab describes how the set reaches GitLab.
type ProjectSetGitLab struct {
	// URL of the GitLab instance, e.g. https://gitlab.com.
	// +kubebuilder:default="https://gitlab.com"
	// +optional
	URL string `json:"url,omitempty"`

	// TokenSecretRef points to an access token with the api and
	// write_repository scopes in every project of the group. The set's
	// children use it too.
	TokenSecretRef corev1.SecretKeySelector `json:"tokenSecretRef"`

	// Group is the full path of the root group, e.g. "my-group". The set
	// discovers the projects of the group and its subgroups.
	// +kubebuilder:validation:MinLength=1
	Group string `json:"group"`
}

// ProjectSetState is what the set does with a matched project.
// +kubebuilder:validation:Enum=active;dormant;excluded;explicit;nameTaken
type ProjectSetState string

const (
	// ProjectSetActive: the project has work and a child AgentProject.
	ProjectSetActive ProjectSetState = "active"
	// ProjectSetDormant: the project has no work; its child, if any, is
	// removed once it has been inactive for dormantAfter.
	ProjectSetDormant ProjectSetState = "dormant"
	// ProjectSetExcluded: exclude matches the project, or it is archived.
	ProjectSetExcluded ProjectSetState = "excluded"
	// ProjectSetExplicit: an AgentProject the set does not own watches the
	// project; that one wins.
	ProjectSetExplicit ProjectSetState = "explicit"
	// ProjectSetNameTaken: the child's name belongs to an object the set
	// does not own.
	ProjectSetNameTaken ProjectSetState = "nameTaken"
)

// ProjectSetProjectStatus is one project the set's include matched.
type ProjectSetProjectStatus struct {
	// Path is the project's full path.
	Path string `json:"path"`

	State ProjectSetState `json:"state"`

	// Child is the name of the child AgentProject, if there is one.
	// +optional
	Child string `json:"child,omitempty"`

	// Reason says why the project is in its state, e.g. "archived",
	// "matches exclude my-group/archived/**" or "watched by AgentProject
	// agents/foo".
	// +optional
	Reason string `json:"reason,omitempty"`

	// LastActiveTime is when the set last saw work in the project.
	// +optional
	LastActiveTime *metav1.Time `json:"lastActiveTime,omitempty"`

	// Milestones is a hash of the project's active milestones (iid, title,
	// updatedAt): a change wakes the child, or the explicit AgentProject
	// that watches the project.
	// +optional
	Milestones string `json:"milestones,omitempty"`

	// MilestonesTruncated: the project has more active milestones than
	// discovery reads, so the hash may miss a change and the project's
	// AgentProject reads its milestones itself.
	// +optional
	MilestonesTruncated bool `json:"milestonesTruncated,omitempty"`

	// HookID is the project hook the set registered on a project without
	// an AgentProject, so a new milestone or ready issue wakes the set.
	// +optional
	HookID int64 `json:"hookID,omitempty"`

	// HookCheckedAt is when the set last checked that hook in GitLab.
	// +optional
	HookCheckedAt *metav1.Time `json:"hookCheckedAt,omitempty"`
}

// ProjectSetCounts counts the set's projects by state.
type ProjectSetCounts struct {
	// +optional
	Active int32 `json:"active"`
	// +optional
	Dormant int32 `json:"dormant"`
	// +optional
	Excluded int32 `json:"excluded"`
	// +optional
	Explicit int32 `json:"explicit"`
	// +optional
	NameTaken int32 `json:"nameTaken"`
}

// ProjectSetComplexity is the GraphQL query complexity of the last
// discovery: the set is Ready=False, reason TooComplex, near the limit.
type ProjectSetComplexity struct {
	Score int32 `json:"score"`
	Limit int32 `json:"limit"`
}

// AgentProjectSetStatus is what the set's last discovery found.
type AgentProjectSetStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastDiscoveryTime is when the set last listed the group's projects.
	// +optional
	LastDiscoveryTime *metav1.Time `json:"lastDiscoveryTime,omitempty"`

	// +optional
	Counts ProjectSetCounts `json:"counts,omitempty"`

	// Projects are the projects include matched, with their state.
	// +optional
	Projects []ProjectSetProjectStatus `json:"projects,omitempty"`

	// DroppedTargets are template crossRepo targets the set's exclude
	// matches: the set drops them from its children.
	// +optional
	DroppedTargets []string `json:"droppedTargets,omitempty"`

	// +optional
	Complexity *ProjectSetComplexity `json:"complexity,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=aps
// +kubebuilder:printcolumn:name="Group",type=string,JSONPath=`.spec.gitlab.group`
// +kubebuilder:printcolumn:name="Active",type=integer,JSONPath=`.status.counts.active`
// +kubebuilder:printcolumn:name="Dormant",type=integer,JSONPath=`.status.counts.dormant`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Last Discovery",type=date,JSONPath=`.status.lastDiscoveryTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AgentProjectSet makes the operator work in the projects of a GitLab group.
type AgentProjectSet struct {
	metav1.TypeMeta   `json:",inline" tstype:",extends"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentProjectSetSpec   `json:"spec"`
	Status AgentProjectSetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// AgentProjectSetList contains a list of AgentProjectSet.
type AgentProjectSetList struct {
	metav1.TypeMeta `json:",inline" tstype:",extends"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentProjectSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentProjectSet{}, &AgentProjectSetList{})
}

const (
	// LabelProjectSet is the label on a set's child AgentProjects: the set's
	// name.
	LabelProjectSet = "ai.wiemers.me/project-set"
	// AnnotationTemplateHash is the hash of the template a child was last
	// written from.
	AnnotationTemplateHash = "ai.wiemers.me/template-hash"
	// AnnotationRemoving marks a dormant child the set is removing: its
	// webhook is switched off, and it is deleted once the hook is gone.
	AnnotationRemoving = "ai.wiemers.me/removing"
)

// The AgentProjectSet conditions and their reasons. One block: tygo would
// turn a block of only Condition* constants into a TypeScript union named
// Condition, which clashes with the Condition interface.
const (
	// ConditionReady is the AgentProjectSet condition that tells whether the
	// spec and template are usable and the query fits the complexity limit.
	ConditionReady = "Ready"
	// ConditionDiscovered is the AgentProjectSet condition that tells
	// whether the last discovery succeeded.
	ConditionDiscovered = "Discovered"

	ReasonInvalidSpec     = "InvalidSpec"
	ReasonInvalidTemplate = "InvalidTemplate"
	ReasonWebhookRequired = "WebhookRequired"
	ReasonTooComplex      = "TooComplex"
	ReasonDiscoveryFailed = "DiscoveryFailed"
	ReasonReady           = "Ready"
)

const (
	DefaultProjectSetPollInterval = 2 * time.Minute
	DefaultProjectSetDormantAfter = 168 * time.Hour
)

// WithDefaults returns a copy of the spec with the defaults the API server
// also sets filled in.
func (s AgentProjectSetSpec) WithDefaults() AgentProjectSetSpec {
	if s.GitLab.URL == "" {
		s.GitLab.URL = "https://gitlab.com"
	}
	if s.PollInterval.Duration <= 0 {
		s.PollInterval.Duration = DefaultProjectSetPollInterval
	}
	if s.DormantAfter.Duration <= 0 {
		s.DormantAfter.Duration = DefaultProjectSetDormantAfter
	}
	return s
}

// Validate returns one problem per broken field: an empty group, an empty
// include, and each include or exclude glob that does not parse.
func (s AgentProjectSetSpec) Validate() []string {
	var problems []string
	if strings.TrimSpace(s.GitLab.Group) == "" {
		problems = append(problems, "gitlab.group is empty")
	}
	if len(s.Include) == 0 {
		problems = append(problems, "include is empty")
	}
	for i, g := range s.Include {
		if err := projectglob.Validate(g); err != nil {
			problems = append(problems, fmt.Sprintf("include[%d]: %v", i, err))
		}
	}
	for i, g := range s.Exclude {
		if err := projectglob.Validate(g); err != nil {
			problems = append(problems, fmt.Sprintf("exclude[%d]: %v", i, err))
		}
	}
	return append(problems, s.entryProblems()...)
}

// entryProblems returns one problem per broken entry of projects: an empty
// path, a glob, a path not below the group, a duplicate path (in any case),
// a name that is no DNS label, and a duplicate name.
func (s AgentProjectSetSpec) entryProblems() []string {
	var problems []string
	group := NormalizeProject(s.GitLab.Group)
	paths, names := map[string]int{}, map[string]int{}
	for i, e := range s.Projects {
		path := NormalizeProject(e.Path)
		first, dupPath := paths[path]
		var problem string
		switch {
		case path == "":
			problem = "path is empty"
		case strings.Contains(path, "*"):
			problem = fmt.Sprintf("path %q is a glob, name one project", e.Path)
		case group != "" && !strings.HasPrefix(path, group+"/"):
			problem = fmt.Sprintf("path %q is not below gitlab.group %q", e.Path, s.GitLab.Group)
		case dupPath:
			problem = fmt.Sprintf("path %q is already projects[%d]", e.Path, first)
		default:
			paths[path] = i
			problem = entryNameProblem(e.Name, names, i)
		}
		if problem != "" {
			problems = append(problems, fmt.Sprintf("projects[%d]: %s", i, problem))
		}
	}
	return problems
}

// entryNameProblem checks entry i's optional child name against the names
// of the entries before it, and records it.
func entryNameProblem(name string, names map[string]int, i int) string {
	if name == "" {
		return ""
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Sprintf("name %q: %s", name, strings.Join(errs, "; "))
	}
	if first, dup := names[name]; dup {
		return fmt.Sprintf("name %q is already projects[%d]", name, first)
	}
	names[name] = i
	return ""
}

// DecodeTemplate decodes the template strictly into an AgentProjectSpec:
// unknown fields and a gitlab key are refused. A missing template is the
// empty spec. It applies no AgentProject defaults; the API server defaults
// the children.
func (s AgentProjectSetSpec) DecodeTemplate() (AgentProjectSpec, error) {
	return decodeStrict("template", s.Template)
}

// DecodeEntries decodes each entry's spec alone, strictly, as DecodeTemplate
// decodes the template. Null values pass: they remove template keys. The
// error names the entry, e.g. "projects[1].spec: …".
func (s AgentProjectSetSpec) DecodeEntries() ([]AgentProjectSpec, error) {
	specs := make([]AgentProjectSpec, 0, len(s.Projects))
	for i, e := range s.Projects {
		spec, err := decodeStrict(fmt.Sprintf("projects[%d].spec", i), e.Spec)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// decodeStrict decodes raw, the value of field, strictly into an
// AgentProjectSpec: invalid JSON, a non-object, unknown fields and a gitlab
// key are refused. Missing or empty raw is the empty spec. The error starts
// with field.
func decodeStrict(field string, raw *runtime.RawExtension) (AgentProjectSpec, error) {
	var spec AgentProjectSpec
	if raw == nil || len(bytes.TrimSpace(raw.Raw)) == 0 {
		return spec, nil
	}
	// Unmarshal refuses invalid JSON, a non-object and trailing data.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw.Raw, &top); err != nil {
		return spec, fmt.Errorf("%s: %w", field, err)
	}
	if _, ok := top["gitlab"]; ok {
		return spec, fmt.Errorf("%s.gitlab is set by the set", field)
	}
	dec := json.NewDecoder(bytes.NewReader(raw.Raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return AgentProjectSpec{}, fmt.Errorf("%s: %w", field, err)
	}
	return spec, nil
}

// WebhookProblem says what the template's webhook lacks for a set child:
// webhook.enabled, webhook.secretRef and webhook.url. Empty when nothing.
func (s AgentProjectSetSpec) WebhookProblem(t AgentProjectSpec) string {
	var missing []string
	if !t.Webhook.Enabled {
		missing = append(missing, "template.webhook.enabled is not true")
	}
	if t.Webhook.SecretRef == nil {
		missing = append(missing, "template.webhook.secretRef is not set")
	}
	if t.Webhook.URL == "" {
		missing = append(missing, "template.webhook.url is not set")
	}
	return strings.Join(missing, "; ")
}
