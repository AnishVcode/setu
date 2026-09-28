package eval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

// Determination is the complete output of a rule evaluation: everything spec
// 5.5.1 requires the engine to produce, plus the evidence for it.
//
// It is a value, not a view. The applicant console, the officer console, the
// case service and the compliance twin all consume this same structure, so
// there is exactly one answer to "what does the law require here" and one
// place it is computed.
type Determination struct {
	// Provenance: enough to reconstruct this determination years later (P6).
	Package        string     `json:"package"`
	RulesetVersion string     `json:"rulesetVersion"`
	RulesetHash    string     `json:"rulesetHash"`
	ProfileID      string     `json:"profileId"`
	ProfileVersion int        `json:"profileVersion"`
	AsOf           value.Date `json:"asOf"`
	Language       string     `json:"language"`
	LayersVersion  string     `json:"layersVersion,omitempty"`

	Approvals   []ApplicableApproval  `json:"approvals"`
	Documents   []DocumentRequirement `json:"documents"`
	Fees        []FeeLine             `json:"fees"`
	FeeTotal    string                `json:"feeTotal"`
	FeeComplete bool                  `json:"feeComplete"`
	DAG         DependencyGraph       `json:"dag"`
	Obligations []ObligationInstance  `json:"obligations"`
	Incentives  []IncentiveMatch      `json:"incentives"`

	// Unresolved is the honest half of the output: the questions whose answers
	// would change the result, each with the exact wording to put to the
	// applicant. An empty list means the determination is complete.
	Unresolved []UnresolvedDetermination `json:"unresolved"`

	// Conflicts are contradictions in the encoded law that this profile
	// happened to expose. They are never resolved silently; each one routes to
	// a human and is a defect against the regulatory content team (spec 13).
	Conflicts []Conflict `json:"conflicts"`

	// Flags are risk signals raised by rules, consumed by the Risk service.
	Flags []string `json:"flags"`

	// Trace is the derivation: every rule, whether it fired, and why.
	Trace []RuleTrace `json:"trace,omitempty"`

	// Hash is a content hash over everything above. Two evaluations of the same
	// profile against the same ruleset must produce the same hash; the
	// determinism test asserts exactly that.
	Hash string `json:"hash"`

	statuses map[string]*ApprovalStatus
}

// ApprovalStatus is the settled outcome for one approval type.
type ApprovalStatus struct {
	ID       string    `json:"id"`
	Required value.Tri `json:"-"`
	// RequiredText renders the tri-state for consumers that are not Go.
	RequiredText string `json:"required"`
	Exempted     bool   `json:"exempted"`

	// DecidedBy names the rule that determined the outcome, and Precedence the
	// level at which it did, so a citizen can see which provision governs when
	// a general rule and a sector exemption both apply (spec 5.5.3).
	DecidedBy  string `json:"decidedBy,omitempty"`
	Precedence int    `json:"precedence,omitempty"`

	// ByDependency names the approval whose prerequisite this is, when the
	// requirement arises from the DAG rather than from a rule of its own.
	ByDependency string `json:"byDependency,omitempty"`

	// UnresolvedVars are the profile variables whose answers would settle this.
	UnresolvedVars []string `json:"unresolvedVars,omitempty"`

	// BlockedBy names the rules whose own outcome is undetermined and which
	// could still change this one. It is what lets an officer or an author see
	// why a seemingly settled question is being held open.
	BlockedBy []string `json:"blockedBy,omitempty"`

	// Contributions lists every rule that acted on this approval, fired or not,
	// which is what makes an "why not?" question answerable.
	Contributions []Contribution `json:"contributions,omitempty"`
}

// Contribution is one rule's effect on one approval.
type Contribution struct {
	RuleID     string `json:"ruleId"`
	Effect     string `json:"effect"` // require | exempt
	Precedence int    `json:"precedence"`
	Fired      string `json:"fired"` // true | false | unknown
	Overridden bool   `json:"overridden,omitempty"`
}

// ApplicableApproval is one approval the applicant must obtain.
type ApplicableApproval struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Summary        string `json:"summary,omitempty"`
	Category       string `json:"category,omitempty"`
	Department     string `json:"department"`
	DepartmentName string `json:"departmentName,omitempty"`

	// Reason is the rendered, localised explanation with the applicant's own
	// values interpolated — the answer to "why do I need this?" (P5).
	Reason       string `json:"reason"`
	ReasonRuleID string `json:"reasonRuleId,omitempty"`

	// LegalBasis is the citation, always present: spec 17 makes a determination
	// without a citation a defect.
	LegalBasis string `json:"legalBasis"`

	SLADays   int    `json:"slaDays"`
	SLA       string `json:"sla"`
	Validity  string `json:"validity,omitempty"`
	RenewalIn string `json:"renewalIn,omitempty"`

	DeemedPermitted bool   `json:"deemedApprovalPermitted"`
	DeemedSection   string `json:"deemedApprovalSection,omitempty"`

	AppealAuthority string `json:"appealAuthority,omitempty"`
	AppealWithin    string `json:"appealWithin,omitempty"`

	DependsOn   []Dependency `json:"dependsOn,omitempty"`
	Documents   []string     `json:"documents,omitempty"`
	Fees        []string     `json:"fees,omitempty"`
	Obligations []string     `json:"obligations,omitempty"`

	// ByDependency is set when this approval is required only because another
	// approval needs it first.
	ByDependency string `json:"byDependency,omitempty"`
}

// Dependency is an edge shown to an applicant whose case is waiting.
type Dependency struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Reason string `json:"reason,omitempty"`
}

// DocumentRequirement is one document, deduplicated across every approval that
// needs it. Collecting it once and reusing it everywhere is the whole point of
// P2, so the engine never emits the same document twice.
type DocumentRequirement struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Issuer       string   `json:"issuer,omitempty"`
	Verification string   `json:"verification"`
	SourceSystem string   `json:"sourceSystem,omitempty"`
	MaxAge       string   `json:"maxAge,omitempty"`
	Credential   string   `json:"credential,omitempty"`
	Attributes   []string `json:"attributes,omitempty"`
	RequiredFor  []string `json:"requiredFor"`
}

// FeeLine is one payable fee, attributed to an approval and a treasury head so
// that a single consolidated payment can be split correctly (spec 5.11).
type FeeLine struct {
	FeeID      string `json:"feeId"`
	Title      string `json:"title"`
	ApprovalID string `json:"approvalId"`
	Department string `json:"department,omitempty"`
	Head       string `json:"head,omitempty"`
	Amount     string `json:"amount"`
	AmountRaw  string `json:"amountRaw,omitempty"` // exact rational, for the ledger
	Computed   bool   `json:"computed"`
	// Unresolved names the questions that must be answered before this fee can
	// be computed. An uncomputable fee is shown as such, never as zero.
	Unresolved []string `json:"unresolved,omitempty"`
	LegalBasis string   `json:"legalBasis,omitempty"`
}

// ObligationInstance is a recurring duty created by holding an approval. These
// become the Compliance Twin's calendar entries (spec 5.13).
type ObligationInstance struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	ApprovalID  string   `json:"approvalId"`
	Department  string   `json:"department,omitempty"`
	Cadence     string   `json:"cadence,omitempty"`
	DueMonth    int      `json:"dueMonth,omitempty"`
	DueDay      int      `json:"dueDay,omitempty"`
	DueWithin   string   `json:"dueWithin,omitempty"`
	Evidence    []string `json:"evidence,omitempty"`
	Consequence string   `json:"consequence,omitempty"`
	LegalBasis  string   `json:"legalBasis,omitempty"`
}

// IncentiveMatch is a scheme the applicant appears eligible for.
type IncentiveMatch struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Scheme     string   `json:"scheme,omitempty"`
	Department string   `json:"department,omitempty"`
	Value      string   `json:"value,omitempty"`
	Computed   bool     `json:"computed"`
	Unresolved []string `json:"unresolved,omitempty"`
	WindowFrom string   `json:"windowFrom,omitempty"`
	WindowTo   string   `json:"windowTo,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
	ApplyURL   string   `json:"applyUrl,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	RuleID     string   `json:"ruleId,omitempty"`
	LegalBasis string   `json:"legalBasis,omitempty"`
}

// UnresolvedDetermination is a question the engine needs answered, together
// with what it would settle. This is the structure that makes "the engine must
// not guess" actionable rather than merely a prohibition.
type UnresolvedDetermination struct {
	Var      string `json:"var"`
	Question string `json:"question"`
	Hint     string `json:"hint,omitempty"`
	Example  string `json:"example,omitempty"`
	Type     string `json:"type,omitempty"`
	// Affects names what is undetermined without this answer.
	AffectsApprovals  []string `json:"affectsApprovals,omitempty"`
	AffectsFees       []string `json:"affectsFees,omitempty"`
	AffectsIncentives []string `json:"affectsIncentives,omitempty"`
}

// Conflict is a contradiction between rules of equal precedence that this
// profile exposed.
type Conflict struct {
	ApprovalID   string   `json:"approvalId"`
	Precedence   int      `json:"precedence"`
	RequireRules []string `json:"requireRules"`
	ExemptRules  []string `json:"exemptRules"`
	Resolution   string   `json:"resolution"`
}

// RuleTrace records one rule's evaluation.
type RuleTrace struct {
	RuleID     string      `json:"ruleId"`
	Title      string      `json:"title,omitempty"`
	Kind       string      `json:"kind"`
	Precedence int         `json:"precedence"`
	Fired      string      `json:"fired"`
	InForce    bool        `json:"inForce"`
	Condition  string      `json:"condition,omitempty"`
	LegalBasis string      `json:"legalBasis,omitempty"`
	Unresolved []string    `json:"unresolved,omitempty"`
	Detail     []TraceNode `json:"detail,omitempty"`
	Effects    []string    `json:"effects,omitempty"`
}

// DependencyGraph is the approval DAG with its critical path and forecast.
type DependencyGraph struct {
	Nodes []DAGNode `json:"nodes"`
	Edges []DAGEdge `json:"edges"`
	// CriticalPath is the chain of approvals that determines the finish date.
	// Showing it is what turns a status tracker into an orchestrator (spec 5.7).
	CriticalPath []string `json:"criticalPath"`
	// TotalDays is the forecast elapsed time if every approval takes its full
	// statutory SLA and independent approvals run in parallel.
	TotalDays int `json:"totalDays"`
	// SequentialDays is the same set run one after another, for the comparison
	// that justifies parallel dispatch.
	SequentialDays int `json:"sequentialDays"`
	// Waves groups approvals that can be dispatched simultaneously.
	Waves [][]string `json:"waves"`
}

// DAGNode is one approval in the dependency graph.
type DAGNode struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	SLADays     int    `json:"slaDays"`
	EarliestDay int    `json:"earliestStartDay"`
	FinishDay   int    `json:"finishDay"`
	Critical    bool   `json:"critical"`
	Wave        int    `json:"wave"`
}

// DAGEdge is a prerequisite relation.
type DAGEdge struct {
	From   string `json:"from"` // the prerequisite
	To     string `json:"to"`   // the approval that waits for it
	Reason string `json:"reason,omitempty"`
}

// StatusOf returns the settled status of an approval.
func (d *Determination) StatusOf(id string) (*ApprovalStatus, bool) {
	s, ok := d.statuses[id]
	return s, ok
}

// Statuses lists every approval's status, sorted by identifier.
func (d *Determination) Statuses() []*ApprovalStatus {
	ids := make([]string, 0, len(d.statuses))
	for k := range d.statuses {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	out := make([]*ApprovalStatus, 0, len(ids))
	for _, id := range ids {
		out = append(out, d.statuses[id])
	}
	return out
}

// ApprovalIDs lists the required approvals, sorted.
func (d *Determination) ApprovalIDs() []string {
	out := make([]string, 0, len(d.Approvals))
	for _, a := range d.Approvals {
		out = append(out, a.ID)
	}
	sort.Strings(out)
	return out
}

// IsComplete reports whether the determination left nothing unresolved.
func (d *Determination) IsComplete() bool {
	return len(d.Unresolved) == 0 && len(d.Conflicts) == 0
}

// JSON renders the determination canonically. Struct field order is fixed and
// every slice is sorted before this point, so the output is byte-stable.
func (d *Determination) JSON() ([]byte, error) { return json.MarshalIndent(d, "", "  ") }

// computeHash sets Hash to a digest over the determination with the hash field
// itself excluded, so the value is self-verifying.
func (d *Determination) computeHash() {
	d.Hash = ""
	b, err := json.Marshal(d)
	if err != nil {
		return
	}
	sum := sha256.Sum256(b)
	d.Hash = hex.EncodeToString(sum[:])
}

// renderLocalised picks the requested language with the documented fallback.
func renderLocalised(t srl.LocalisedText, lang string) string { return t.Get(lang) }
