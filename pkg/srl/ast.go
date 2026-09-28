package srl

import (
	"math/big"
	"strings"

	"github.com/setu-platform/setu/pkg/value"
)

// File is one parsed SRL source file.
type File struct {
	Path        string
	Package     string // the jurisdiction this content binds to, e.g. IN.TN
	Meta        Block
	Enums       []*EnumDecl
	Profile     *ProfileDecl
	Departments []*DepartmentDecl
	Documents   []*DocumentDecl
	Approvals   []*ApprovalDecl
	Fees        []*FeeDecl
	Obligations []*ObligationDecl
	Incentives  []*IncentiveDecl
	Rules       []*RuleDecl
}

// Block is a bag of free-form key/value metadata, used where the schema is
// deliberately open (the `meta` header).
type Block map[string]string

// LocalisedText holds one string per language tag. Spec P10 makes complete
// language coverage structural, so every citizen-visible string in the language
// is a map, never a bare string, and the linter checks coverage.
type LocalisedText map[string]string

// Get returns the text for lang, falling back to English and then to any
// available language, so a partially translated ruleset degrades to something
// readable rather than to an empty screen.
func (lt LocalisedText) Get(lang string) string {
	if s, ok := lt[lang]; ok {
		return s
	}
	if s, ok := lt["en"]; ok {
		return s
	}
	for _, k := range lt.Langs() {
		return lt[k]
	}
	return ""
}

// Langs lists the languages present, sorted.
func (lt LocalisedText) Langs() []string {
	out := make([]string, 0, len(lt))
	for k := range lt {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// LegalBasis is the citation every regulatory object must carry. Spec P5 makes
// this a product feature: a citizen sees the statute behind each requirement,
// so it is a required field on approvals, rules, fees and obligations, and the
// linter refuses content without it.
type LegalBasis struct {
	Act          string
	Section      string
	Rule         string
	Notification string
	Dated        value.Date
	URL          string
	Pos          Pos
}

// Cite renders the citation as it appears to a citizen.
func (lb LegalBasis) Cite() string {
	var parts []string
	if lb.Act != "" {
		parts = append(parts, lb.Act)
	}
	if lb.Section != "" {
		parts = append(parts, "Section "+lb.Section)
	}
	if lb.Rule != "" {
		parts = append(parts, "Rule "+lb.Rule)
	}
	if lb.Notification != "" {
		n := "Notification " + lb.Notification
		if !lb.Dated.IsZero() {
			n += " dated " + lb.Dated.String()
		}
		parts = append(parts, n)
	}
	return strings.Join(parts, ", ")
}

// IsEmpty reports whether no citation was given at all.
func (lb LegalBasis) IsEmpty() bool {
	return lb.Act == "" && lb.Section == "" && lb.Rule == "" && lb.Notification == ""
}

// Governance is the ownership and review metadata spec 13 requires on every
// piece of regulatory content: a named owner, a review cadence, a last-verified
// date, and a review status that gates publication.
type Governance struct {
	Owner         string
	ReviewStatus  ReviewStatus
	LastVerified  value.Date
	ReviewCadence value.Period
	Pos           Pos
}

// ReviewStatus records how far a piece of content has moved through the
// four-eyes workflow of spec 5.5.5.
type ReviewStatus uint8

const (
	// StatusUnverified is the default. Content in this state is authored but
	// not legally certified, and MUST NOT reach a production jurisdiction.
	StatusUnverified ReviewStatus = iota
	// StatusInReview is with a legal reviewer.
	StatusInReview
	// StatusVerified has been signed off by the named department owner.
	StatusVerified
	// StatusDeprecated is retained for the reconstruction of past cases only.
	StatusDeprecated
)

var reviewStatusNames = map[ReviewStatus]string{
	StatusUnverified: "unverified", StatusInReview: "in_review",
	StatusVerified: "verified", StatusDeprecated: "deprecated",
}

func (r ReviewStatus) String() string { return reviewStatusNames[r] }

// ParseReviewStatus reads a review status keyword.
func ParseReviewStatus(s string) (ReviewStatus, bool) {
	for k, v := range reviewStatusNames {
		if v == s {
			return k, true
		}
	}
	return StatusUnverified, false
}

// Validity is the effective-dated interval every regulatory object carries (P6).
type Validity struct {
	From  value.Date
	Until value.Date // zero means open-ended
}

// Covers reports whether the object is in force on a given date.
func (v Validity) Covers(d value.Date) bool { return d.InInterval(v.From, v.Until) }

// ---------- declarations ----------

// EnumDecl declares a closed vocabulary, e.g. the districts of a state or the
// pollution categories of a state board.
type EnumDecl struct {
	Name    string
	Members []EnumMember
	Pos     Pos
}

// EnumMember is one member with its display text.
type EnumMember struct {
	Name  string
	Title LocalisedText
	Pos   Pos
}

// ProfileDecl declares the Project Profile schema: the complete set of
// questions the platform may ask an applicant, and therefore the complete input
// domain of the rules engine.
type ProfileDecl struct {
	Vars []*VarDecl
	Pos  Pos
}

// VarDecl is one profile variable.
//
// Question and Hint are not documentation. When a rule cannot be decided
// because this variable is unanswered, the engine returns the question verbatim
// as the unresolved determination (spec 5.5.1 item 7), so the applicant is
// asked exactly what the law needs to know.
type VarDecl struct {
	Name     string
	Type     TypeExpr
	Question LocalisedText
	Hint     LocalisedText
	Unit     string // preferred display unit for quantities
	Example  string
	// Group names the section of the form this question belongs in. Thirty
	// questions in one flat list is not a form anyone completes, and how they
	// are grouped is part of how they are put to an applicant — so it is
	// authored here beside the wording, not hardcoded in a client.
	Group     LocalisedText
	Sensitive bool // excluded from analytics exports and risk features
	Pos       Pos
}

// TypeExpr is a syntactic type as written in a profile declaration.
type TypeExpr struct {
	Name  string    // Bool, Number, Text, Date, Period, Money, Quantity, Enum, Set, Geo
	Param string    // dimension name for Quantity, enum name for Enum
	Elem  *TypeExpr // element type for Set
	Pos   Pos
}

func (t TypeExpr) String() string {
	switch {
	case t.Elem != nil:
		return t.Name + "<" + t.Elem.String() + ">"
	case t.Param != "":
		return t.Name + "<" + t.Param + ">"
	}
	return t.Name
}

// DepartmentDecl declares a statutory authority.
type DepartmentDecl struct {
	ID           string
	Name         LocalisedText
	Jurisdiction string
	AdapterTier  string // api | file | rpa | assisted_manual
	ContactURL   string
	Governance   Governance
	Pos          Pos
}

// DocumentDecl declares a document or verified attribute the platform may need.
type DocumentDecl struct {
	ID           string
	Title        LocalisedText
	Issuer       string
	Verification VerificationPathway
	SourceSystem string       // the adapter that can verify it, when API-verifiable
	MaxAge       value.Period // freshness policy; zero means verify once, permanently
	Credential   string       // the verifiable credential type it produces
	Attributes   []string     // the attributes it establishes, for selective disclosure
	Governance   Governance
	Pos          Pos
}

// VerificationPathway is the priority-ordered set of ways a fact can be
// established, from spec 5.3.
type VerificationPathway uint8

const (
	VerifyAPI VerificationPathway = iota
	VerifyDigiLocker
	VerifyOfficer
	VerifySelfDeclared
)

var verificationNames = map[VerificationPathway]string{
	VerifyAPI: "api", VerifyDigiLocker: "digilocker",
	VerifyOfficer: "officer", VerifySelfDeclared: "self_declared",
}

func (v VerificationPathway) String() string { return verificationNames[v] }

// ParseVerification reads a verification pathway keyword.
func ParseVerification(s string) (VerificationPathway, bool) {
	for k, v := range verificationNames {
		if v == s {
			return k, true
		}
	}
	return VerifyAPI, false
}

// ApprovalDecl declares an approval type: a licence, registration, consent,
// NOC, permit, certificate or intimation.
type ApprovalDecl struct {
	ID           string
	Title        LocalisedText
	Summary      LocalisedText
	Category     string // licence | registration | consent | noc | permit | certificate | intimation
	Department   string
	Jurisdiction string
	SLA          value.Period // the statutory timeline
	Validity     value.Period // zero means perpetual
	RenewNotice  value.Period
	RenewCadence value.Period
	Deemed       DeemedApproval
	Documents    []string
	Fees         []string
	Obligations  []string
	DependsOn    []DependencyRef
	Appeal       *AppealDecl
	LegalBasis   LegalBasis
	Governance   Governance
	Valid        Validity
	Pos          Pos
}

// DependencyRef is an edge in the approval DAG, with the reason shown to the
// applicant when a case sits in WAITING_ON_PREREQUISITE.
type DependencyRef struct {
	Approval string
	Reason   LocalisedText
	Pos      Pos
}

// DeemedApproval records whether the law permits deemed approval for this
// approval type, and under which provision. Spec P3 and 5.7 require the
// enabling clause to be recorded on the order itself, so it is captured here
// rather than in a configuration file.
type DeemedApproval struct {
	Permitted bool
	Section   string
	Pos       Pos
}

// AppealDecl names the statutory appellate authority and the window to appeal.
type AppealDecl struct {
	Authority LocalisedText
	Within    value.Period
	Pos       Pos
}

// FeeDecl declares a fee with its computation and its government account head.
type FeeDecl struct {
	ID         string
	Title      LocalisedText
	Head       string // treasury accounting head, for the Payments service split
	Department string
	Amount     Expr
	Rounding   Rounding
	LegalBasis LegalBasis
	Governance Governance
	Valid      Validity
	Pos        Pos
}

// Rounding fixes how a computed fee is reduced to a payable amount. It is
// explicit because rounding a statutory fee the wrong way is a refund
// reconciliation problem at national scale.
type Rounding struct {
	Mode string   // none | nearest | up | down
	To   *big.Rat // the increment, in rupees
}

// ObligationDecl declares a recurring duty created by holding an approval.
type ObligationDecl struct {
	ID          string
	Title       LocalisedText
	Cadence     value.Period
	DueMonth    int // 1-12, for annual obligations on a fixed date
	DueDay      int
	DueWithin   value.Period // alternatively, due within a period of the trigger
	Evidence    []string
	Consequence LocalisedText
	Department  string
	LegalBasis  LegalBasis
	Governance  Governance
	Valid       Validity
	Pos         Pos
}

// IncentiveDecl declares a scheme entitlement.
type IncentiveDecl struct {
	ID         string
	Title      LocalisedText
	Scheme     string
	Department string
	Value      Expr // estimated entitlement value
	Window     Validity
	Evidence   []string
	ApplyURL   string
	LegalBasis LegalBasis
	Governance Governance
	Pos        Pos
}

// RuleKind classifies what a rule does, so that Rule Studio can group them and
// the analyser can apply the right conflict checks.
type RuleKind uint8

const (
	RuleTrigger RuleKind = iota
	RuleExemption
	RuleDocument
	RuleFee
	RuleObligation
	RuleIncentive
	RuleDependency
)

var ruleKindNames = map[RuleKind]string{
	RuleTrigger: "trigger", RuleExemption: "exemption", RuleDocument: "document",
	RuleFee: "fee", RuleObligation: "obligation", RuleIncentive: "incentive",
	RuleDependency: "dependency",
}

func (r RuleKind) String() string { return ruleKindNames[r] }

// ParseRuleKind reads a rule kind keyword.
func ParseRuleKind(s string) (RuleKind, bool) {
	for k, v := range ruleKindNames {
		if v == s {
			return k, true
		}
	}
	return RuleTrigger, false
}

// RuleDecl is the unit of encoded law.
type RuleDecl struct {
	ID         string
	Title      LocalisedText
	Kind       RuleKind
	Precedence int // higher wins; exemptions sit above the rules they displace
	Overrides  []string
	When       Expr
	Then       []Effect
	Explain    LocalisedText
	Tests      []*TestDecl
	LegalBasis LegalBasis
	Governance Governance
	Valid      Validity
	Tags       []string
	Pos        Pos
}

// DefaultPrecedence is the precedence of a rule that does not state one.
const DefaultPrecedence = 100

// TestDecl is an authored test case. Spec 5.5.5 forbids publishing a rule
// without both a positive and a negative case, and the linter enforces it.
type TestDecl struct {
	Name   string
	Given  []Assignment
	Expect TestExpectation
	Pos    Pos
}

// Assignment binds a profile variable in a test case.
type Assignment struct {
	Var string
	Val Expr
	Pos Pos
}

// TestExpectation is what the author asserts about the rule under test.
type TestExpectation struct {
	Fires value.Tri // True: expect fires; False: expect not fires; Unknown: expect undetermined
	Pos   Pos
}

// EffectKind is the sort of consequence a rule produces.
type EffectKind uint8

const (
	EffectRequireApproval EffectKind = iota
	EffectExemptApproval
	EffectRequireDocument
	EffectLevyFee
	EffectWaiveFee
	EffectCreateObligation
	EffectOfferIncentive
	EffectDependency
	EffectFlag
)

var effectKindNames = map[EffectKind]string{
	EffectRequireApproval: "require approval", EffectExemptApproval: "exempt approval",
	EffectRequireDocument: "require document", EffectLevyFee: "levy fee",
	EffectWaiveFee: "waive fee", EffectCreateObligation: "create obligation",
	EffectOfferIncentive: "offer incentive", EffectDependency: "depend",
	EffectFlag: "flag",
}

func (e EffectKind) String() string { return effectKindNames[e] }

// Effect is one consequence in a rule's `then` block.
type Effect struct {
	Kind   EffectKind
	Target string // the approval, document, fee, obligation or incentive ID
	On     string // the approval a document, fee or obligation attaches to
	Reason LocalisedText
	Flag   string
	Pos    Pos
}

// ---------- expressions ----------

// Expr is a rule condition or a fee formula.
type Expr interface {
	Position() Pos
	exprNode()
}

// NumberLit is a dimensionless exact number.
type NumberLit struct {
	Val *big.Rat
	Pos Pos
}

// QuantityLit is a dimensioned number, e.g. `60 KLD`.
type QuantityLit struct {
	Val  *big.Rat
	Unit string
	Pos  Pos
}

// MoneyLit is an amount, e.g. `INR 4 crore`.
type MoneyLit struct {
	Val *big.Rat
	Pos Pos
}

// PeriodLit is a calendar period, e.g. `30 days`.
type PeriodLit struct {
	Val value.Period
	Pos Pos
}

// StringLit is a text literal.
type StringLit struct {
	Val string
	Pos Pos
}

// BoolLit is `true` or `false`.
type BoolLit struct {
	Val bool
	Pos Pos
}

// UnknownLit is the literal `unknown`, used in tests to assert that a rule is
// undetermined on a given profile.
type UnknownLit struct{ Pos Pos }

// DateLit is a civil date literal.
type DateLit struct {
	Val value.Date
	Pos Pos
}

// SetLit is a set literal, e.g. `[10611, 10612]`.
type SetLit struct {
	Elems []Expr
	Pos   Pos
}

// ProfileRef reads a profile variable, always written with the `profile.`
// prefix so that a reader can never confuse an applicant's declaration with a
// declared regulatory object.
type ProfileRef struct {
	Var string
	Pos Pos
}

// ObjectRef names a declared object: an approval, enum member, document, fee,
// obligation or incentive.
type ObjectRef struct {
	Path []string
	Pos  Pos
}

// Name renders the dotted path.
func (o *ObjectRef) Name() string { return strings.Join(o.Path, ".") }

// UnaryExpr is `not x` or `-x`.
type UnaryExpr struct {
	Op  Kind
	X   Expr
	Pos Pos
}

// BinaryExpr covers logic, comparison and arithmetic.
type BinaryExpr struct {
	Op   Kind
	L, R Expr
	Pos  Pos
}

// InExpr is set membership.
type InExpr struct {
	X   Expr
	Set Expr
	Pos Pos
}

// CallExpr is a builtin function application.
type CallExpr struct {
	Fn   string
	Args []Expr
	Pos  Pos
}

// IfExpr is a conditional expression, used mainly in fee formulas.
type IfExpr struct {
	Cond Expr
	Then Expr
	Else Expr
	Pos  Pos
}

// SlabExpr is a piecewise table, the shape almost every statutory fee actually
// takes. Writing it as nested conditionals obscures the schedule; writing it as
// a slab keeps the source looking like the notification it came from.
type SlabExpr struct {
	Subject Expr
	Arms    []SlabArm
	Default Expr
	Pos     Pos
}

// SlabArm is one row of a fee schedule: an operator, a bound, and the amount.
type SlabArm struct {
	Op    Kind // LE, LT, GE, GT, EQ
	Bound Expr
	Val   Expr
	Pos   Pos
}

func (n *NumberLit) Position() Pos   { return n.Pos }
func (n *QuantityLit) Position() Pos { return n.Pos }
func (n *MoneyLit) Position() Pos    { return n.Pos }
func (n *PeriodLit) Position() Pos   { return n.Pos }
func (n *StringLit) Position() Pos   { return n.Pos }
func (n *BoolLit) Position() Pos     { return n.Pos }
func (n *UnknownLit) Position() Pos  { return n.Pos }
func (n *DateLit) Position() Pos     { return n.Pos }
func (n *SetLit) Position() Pos      { return n.Pos }
func (n *ProfileRef) Position() Pos  { return n.Pos }
func (n *ObjectRef) Position() Pos   { return n.Pos }
func (n *UnaryExpr) Position() Pos   { return n.Pos }
func (n *BinaryExpr) Position() Pos  { return n.Pos }
func (n *InExpr) Position() Pos      { return n.Pos }
func (n *CallExpr) Position() Pos    { return n.Pos }
func (n *IfExpr) Position() Pos      { return n.Pos }
func (n *SlabExpr) Position() Pos    { return n.Pos }

func (n *NumberLit) exprNode()   {}
func (n *QuantityLit) exprNode() {}
func (n *MoneyLit) exprNode()    {}
func (n *PeriodLit) exprNode()   {}
func (n *StringLit) exprNode()   {}
func (n *BoolLit) exprNode()     {}
func (n *UnknownLit) exprNode()  {}
func (n *DateLit) exprNode()     {}
func (n *SetLit) exprNode()      {}
func (n *ProfileRef) exprNode()  {}
func (n *ObjectRef) exprNode()   {}
func (n *UnaryExpr) exprNode()   {}
func (n *BinaryExpr) exprNode()  {}
func (n *InExpr) exprNode()      {}
func (n *CallExpr) exprNode()    {}
func (n *IfExpr) exprNode()      {}
func (n *SlabExpr) exprNode()    {}

// Walk calls fn for every node in the expression tree, depth first. The
// analyser uses it to find the profile variables a rule reads and the
// approvals it interrogates.
func Walk(e Expr, fn func(Expr)) {
	if e == nil {
		return
	}
	fn(e)
	switch x := e.(type) {
	case *SetLit:
		for _, el := range x.Elems {
			Walk(el, fn)
		}
	case *UnaryExpr:
		Walk(x.X, fn)
	case *BinaryExpr:
		Walk(x.L, fn)
		Walk(x.R, fn)
	case *InExpr:
		Walk(x.X, fn)
		Walk(x.Set, fn)
	case *CallExpr:
		for _, a := range x.Args {
			Walk(a, fn)
		}
	case *IfExpr:
		Walk(x.Cond, fn)
		Walk(x.Then, fn)
		Walk(x.Else, fn)
	case *SlabExpr:
		Walk(x.Subject, fn)
		for _, a := range x.Arms {
			Walk(a.Bound, fn)
			Walk(a.Val, fn)
		}
		Walk(x.Default, fn)
	}
}
