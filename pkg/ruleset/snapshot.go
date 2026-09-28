// Package ruleset publishes and verifies immutable ruleset versions.
//
// A published snapshot is the artefact a case is pinned to at submission. Years
// later it must be possible to answer, exactly: what did the system tell this
// applicant, and why (P6). That requires the snapshot to contain the content
// itself, not a reference to a branch that has since moved — so a snapshot
// embeds every source file, the boundary layers, their hashes, and a signature
// over the lot.
package ruleset

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

// SnapshotFileName is the manifest inside a published version directory.
const SnapshotFileName = "snapshot.json"

// Snapshot is an immutable published ruleset version.
type Snapshot struct {
	SchemaVersion int    `json:"schemaVersion"`
	Package       string `json:"package"`
	Version       string `json:"version"`

	// EffectiveFrom is the date this version becomes the one new cases pin to.
	// Publication and effect are separate events: a ruleset is published, then
	// takes effect on a stated date, which is how a scheduled activation works.
	EffectiveFrom value.Date `json:"effectiveFrom"`
	PublishedAt   string     `json:"publishedAt"`
	PublishedBy   string     `json:"publishedBy"`
	Note          string     `json:"note,omitempty"`

	// Production records whether this passed the strict gate. A snapshot that
	// did not cannot lawfully be served to an applicant.
	Production bool `json:"production"`

	Files       []FileRecord `json:"files"`
	ContentHash string       `json:"contentHash"`

	LayersVersion string `json:"layersVersion,omitempty"`
	LayersHash    string `json:"layersHash,omitempty"`

	Counts     map[string]int `json:"counts"`
	Governance Governance     `json:"governance"`

	// Index lists the identifiers this version defines, so a case referring to
	// an approval can be resolved without loading the content.
	Index Index `json:"index"`

	// Signature is over ContentHash plus the manifest fields, in canonical form.
	SignedBy  string `json:"signedBy,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// FileRecord is one embedded source file.
type FileRecord struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Bytes   int    `json:"bytes"`
	Content string `json:"content"`
}

// Governance summarises the review posture of the content in this version.
type Governance struct {
	RulesVerified    int      `json:"rulesVerified"`
	RulesInReview    int      `json:"rulesInReview"`
	RulesUnverified  int      `json:"rulesUnverified"`
	RulesDeprecated  int      `json:"rulesDeprecated"`
	StaleRules       []string `json:"staleRules,omitempty"`
	DeemedApprovals  []string `json:"deemedApprovalPermitted,omitempty"`
	UnverifiedOwners []string `json:"objectsWithoutOwner,omitempty"`
}

// Index is the identifier catalogue of a version.
type Index struct {
	Approvals   []string `json:"approvals"`
	Rules       []string `json:"rules"`
	Documents   []string `json:"documents"`
	Fees        []string `json:"fees"`
	Obligations []string `json:"obligations"`
	Incentives  []string `json:"incentives"`
	Departments []string `json:"departments"`
	ProfileVars []string `json:"profileVars"`
}

// BuildInput carries everything needed to publish, all supplied by the caller.
// Nothing here is read from the clock or the environment, so a publish is a
// reproducible function of its inputs.
type BuildInput struct {
	Dir           string
	Graph         *kg.Graph
	Layers        *value.Layers
	Version       string
	EffectiveFrom value.Date
	PublishedAt   string
	PublishedBy   string
	Note          string
	Production    bool
	Today         value.Date
}

// Build assembles a snapshot from a checked graph.
func Build(in BuildInput) (*Snapshot, error) {
	if err := ValidateVersion(in.Version); err != nil {
		return nil, err
	}
	if in.PublishedBy == "" {
		return nil, fmt.Errorf("--published-by is required: a published ruleset names the person accountable for it")
	}
	if in.EffectiveFrom.IsZero() {
		return nil, fmt.Errorf("--effective-from is required: a ruleset version takes effect on a stated date")
	}

	s := &Snapshot{
		SchemaVersion: 1,
		Package:       in.Graph.Package,
		Version:       in.Version,
		EffectiveFrom: in.EffectiveFrom,
		PublishedAt:   in.PublishedAt,
		PublishedBy:   in.PublishedBy,
		Note:          in.Note,
		Production:    in.Production,
		Counts:        in.Graph.Counts(),
	}

	files, err := collectFiles(in.Dir)
	if err != nil {
		return nil, err
	}
	s.Files = files

	if in.Layers != nil {
		s.LayersVersion = in.Layers.Version
		b, err := os.ReadFile(filepath.Join(in.Dir, kg.LayersFileName))
		if err == nil {
			sum := sha256.Sum256(b)
			s.LayersHash = hex.EncodeToString(sum[:])
		}
	}

	s.Index = buildIndex(in.Graph)
	s.Governance = buildGovernance(in.Graph, in.Today)
	s.ContentHash = contentHash(s)
	return s, nil
}

// collectFiles reads every source file in load order.
func collectFiles(dir string) ([]FileRecord, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".srl") || d.Name() == kg.LayersFileName {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)

	var out []FileRecord
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			rel = p
		}
		sum := sha256.Sum256(b)
		out = append(out, FileRecord{
			Path:    filepath.ToSlash(rel),
			SHA256:  hex.EncodeToString(sum[:]),
			Bytes:   len(b),
			Content: string(b),
		})
	}
	return out, nil
}

func buildIndex(g *kg.Graph) Index {
	return Index{
		Approvals:   g.ApprovalIDs(),
		Rules:       g.RuleIDs(),
		Documents:   g.DocumentIDs(),
		Fees:        g.FeeIDs(),
		Obligations: g.ObligationIDs(),
		Incentives:  g.IncentiveIDs(),
		Departments: g.DepartmentIDs(),
		ProfileVars: g.VarNames(),
	}
}

func buildGovernance(g *kg.Graph, today value.Date) Governance {
	var gov Governance
	for _, id := range g.RuleIDs() {
		r := g.Rules[id]
		switch r.Governance.ReviewStatus {
		case srl.StatusVerified:
			gov.RulesVerified++
		case srl.StatusInReview:
			gov.RulesInReview++
		case srl.StatusDeprecated:
			gov.RulesDeprecated++
		default:
			gov.RulesUnverified++
		}
		if strings.TrimSpace(r.Governance.Owner) == "" {
			gov.UnverifiedOwners = append(gov.UnverifiedOwners, "rule "+id)
		}
		lv, rc := r.Governance.LastVerified, r.Governance.ReviewCadence
		if !lv.IsZero() && !rc.IsZero() && !today.IsZero() && today.After(lv.AddPeriod(rc)) {
			gov.StaleRules = append(gov.StaleRules, id)
		}
	}
	// Deemed approval is listed explicitly on every snapshot. Spec 15 treats its
	// misuse as a headline risk, so which approvals permit it is a published
	// fact about a ruleset version, not a setting buried in configuration.
	for _, id := range g.ApprovalIDs() {
		a := g.Approvals[id]
		if a.Deemed.Permitted {
			gov.DeemedApprovals = append(gov.DeemedApprovals,
				fmt.Sprintf("%s (%s)", id, a.Deemed.Section))
		}
		if strings.TrimSpace(a.Governance.Owner) == "" {
			gov.UnverifiedOwners = append(gov.UnverifiedOwners, "approval "+id)
		}
	}
	sort.Strings(gov.StaleRules)
	sort.Strings(gov.DeemedApprovals)
	sort.Strings(gov.UnverifiedOwners)
	return gov
}

// contentHash digests the manifest with the hash and signature fields cleared,
// so that a snapshot verifies against itself.
func contentHash(s *Snapshot) string {
	cp := *s
	cp.ContentHash = ""
	cp.Signature = ""
	cp.SignedBy = ""
	b, err := json.Marshal(&cp)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Sign attaches an Ed25519 signature over the content hash.
//
// A published ruleset is a legal instrument: it decides what the state told a
// citizen. Signing it means a later reader can establish that the content was
// not altered after publication, without trusting the store it came from.
func (s *Snapshot) Sign(key ed25519.PrivateKey, signerName string) error {
	if len(key) != ed25519.PrivateKeySize {
		return fmt.Errorf("signing key is %d bytes, expected %d", len(key), ed25519.PrivateKeySize)
	}
	s.SignedBy = signerName
	s.Signature = ""
	s.ContentHash = contentHash(s)
	sig := ed25519.Sign(key, []byte(s.ContentHash))
	s.Signature = hex.EncodeToString(sig)
	return nil
}

// VerifyResult reports what checked out and what did not.
type VerifyResult struct {
	HashMatches      bool
	SignatureChecked bool
	SignatureValid   bool
	FileMismatches   []string
	ComputedHash     string
}

// OK reports whether the snapshot is intact.
func (v VerifyResult) OK() bool {
	if !v.HashMatches || len(v.FileMismatches) > 0 {
		return false
	}
	if v.SignatureChecked && !v.SignatureValid {
		return false
	}
	return true
}

// Verify re-derives the content hash and checks each embedded file against its
// recorded digest.
func (s *Snapshot) Verify(pub ed25519.PublicKey) VerifyResult {
	var res VerifyResult
	for _, f := range s.Files {
		sum := sha256.Sum256([]byte(f.Content))
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			res.FileMismatches = append(res.FileMismatches, f.Path)
		}
	}
	res.ComputedHash = contentHash(s)
	res.HashMatches = res.ComputedHash == s.ContentHash

	if s.Signature != "" && len(pub) == ed25519.PublicKeySize {
		res.SignatureChecked = true
		sig, err := hex.DecodeString(s.Signature)
		if err == nil {
			// Verify against the RECOMPUTED hash, not the one recorded in the
			// file. Checking the recorded hash would let anyone edit the content
			// and leave the hash field untouched, and the signature would still
			// appear valid — the signature must attest to the bytes actually
			// present, not to a claim about them.
			res.SignatureValid = ed25519.Verify(pub, []byte(res.ComputedHash), sig)
		}
	}
	return res
}

// Write persists a snapshot to a version directory.
func (s *Snapshot) Write(root string) (string, error) {
	dir := filepath.Join(root, strings.ReplaceAll(s.Package, ".", "-"), s.Version)
	if _, err := os.Stat(filepath.Join(dir, SnapshotFileName)); err == nil {
		return "", fmt.Errorf("version %s is already published at %s; "+
			"published versions are immutable, so publish a new version instead",
			s.Version, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, SnapshotFileName)
	// 0444: the file is written once and then read-only, which makes an
	// accidental edit to a published version fail loudly.
	if err := os.WriteFile(path, b, 0o444); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads a published snapshot.
func Load(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// Graph rebuilds the knowledge graph from an embedded snapshot, which is how a
// case pinned to an old version is re-evaluated years later.
func (s *Snapshot) Graph() (*kg.Graph, []srl.Diagnostic) {
	var files []*srl.File
	var diags []srl.Diagnostic
	for _, f := range s.Files {
		if !strings.HasSuffix(f.Path, ".srl") {
			continue
		}
		parsed, d := srl.Parse(f.Path, f.Content)
		diags = append(diags, d...)
		if parsed != nil {
			files = append(files, parsed)
		}
	}
	g, d := kg.Build(files)
	return g, append(diags, d...)
}

// ValidateVersion enforces semantic versioning on ruleset versions.
func ValidateVersion(v string) error {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return fmt.Errorf("version %q must be semantic, for example 1.4.0", v)
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return fmt.Errorf("version %q must be semantic, for example 1.4.0", v)
		}
	}
	return nil
}

// CompareVersions orders two semantic versions.
func CompareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3 && i < len(as) && i < len(bs); i++ {
		x, _ := strconv.Atoi(as[i])
		y, _ := strconv.Atoi(bs[i])
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// LatestVersion finds the highest published version for a package.
func LatestVersion(root, pkg string) (string, bool) {
	dir := filepath.Join(root, strings.ReplaceAll(pkg, ".", "-"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	best := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if ValidateVersion(e.Name()) != nil {
			continue
		}
		if best == "" || CompareVersions(e.Name(), best) > 0 {
			best = e.Name()
		}
	}
	return best, best != ""
}
