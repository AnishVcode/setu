package ruleset

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

const src = `
package IN.TEST
profile { var workers: Quantity<Count> { question en "Workers?" unit workers } }
department D.X { name en "Department" jurisdiction IN.TEST adapter_tier api }
approval A.X {
  title en "Licence"
  category "licence"
  department D.X
  sla 30 days
  deemed_approval permitted section "9(4)"
  legal_basis { act "Test Act" section "1" }
  owner "Head of Licensing"
  review_status verified
}
rule R.X {
  kind trigger
  when profile.workers >= 10 workers
  then { require approval A.X }
  explain en "Ten or more workers."
  legal_basis { act "Test Act" section "2" }
  owner "Head of Licensing"
  review_status verified
  test "p" { given { profile.workers = 10 workers } expect fires }
  test "n" { given { profile.workers = 9 workers } expect not fires }
}
`

func writeContent(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rules.srl"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func buildSnapshot(t *testing.T, dir, version string) *Snapshot {
	t.Helper()
	g, diags := kg.LoadDir(dir)
	for _, d := range diags {
		if d.Severity == srl.SevError {
			t.Fatalf("loading content: %s", d)
		}
	}
	s, err := Build(BuildInput{
		Dir: dir, Graph: g, Version: version,
		EffectiveFrom: value.Date{Year: 2026, Month: 10, Day: 1},
		PublishedAt:   "2026-09-04T11:00:00Z",
		PublishedBy:   "Regulatory Content Operations",
		Today:         value.Date{Year: 2026, Month: 9, Day: 4},
	})
	if err != nil {
		t.Fatalf("building snapshot: %v", err)
	}
	return s
}

func TestSnapshotIsSelfSufficient(t *testing.T) {
	dir := writeContent(t)
	s := buildSnapshot(t, dir, "1.0.0")

	// Deleting the source must not affect the ability to reconstruct the
	// ruleset: a case pinned to a version is re-evaluated from the snapshot
	// alone, years after the branch it came from has moved on.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	g, diags := s.Graph()
	for _, d := range diags {
		if d.Severity == srl.SevError {
			t.Fatalf("embedded content does not re-parse: %s", d)
		}
	}
	if len(g.Rules) != 1 || len(g.Approvals) != 1 {
		t.Fatalf("rebuilt graph is wrong: %s", g.CountsString())
	}
	if s.Governance.DeemedApprovals == nil ||
		!strings.Contains(s.Governance.DeemedApprovals[0], "9(4)") {
		t.Errorf("deemed approval provisions are not recorded on the snapshot: %+v",
			s.Governance.DeemedApprovals)
	}
	if s.Governance.RulesVerified != 1 {
		t.Errorf("review posture not recorded: %+v", s.Governance)
	}
}

func TestSignatureDetectsTampering(t *testing.T) {
	dir := writeContent(t)
	s := buildSnapshot(t, dir, "1.0.0")

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Sign(priv, "test key"); err != nil {
		t.Fatal(err)
	}
	if res := s.Verify(pub); !res.OK() {
		t.Fatalf("a freshly signed snapshot should verify: %+v", res)
	}

	// Alter a threshold in the embedded content and leave the recorded hash
	// alone — the attack that a naive verifier would miss.
	for i := range s.Files {
		s.Files[i].Content = strings.Replace(s.Files[i].Content,
			">= 10 workers", ">= 5 workers", 1)
	}
	res := s.Verify(pub)
	if res.OK() {
		t.Fatal("tampered content verified successfully")
	}
	if len(res.FileMismatches) == 0 {
		t.Error("the altered file was not identified")
	}
	if res.HashMatches {
		t.Error("the content hash still matches after tampering")
	}
	if res.SignatureValid {
		t.Error("the signature is still reported valid; it must attest to the bytes " +
			"actually present, not to the hash the file claims")
	}
}

func TestPublishedVersionsAreImmutable(t *testing.T) {
	dir := writeContent(t)
	out := t.TempDir()

	s := buildSnapshot(t, dir, "1.0.0")
	if _, err := s.Write(out); err != nil {
		t.Fatal(err)
	}
	// Republishing the same version must fail rather than overwrite: a case
	// pinned to 1.0.0 must mean the same thing forever.
	if _, err := s.Write(out); err == nil {
		t.Fatal("republishing an existing version succeeded; versions must be immutable")
	} else if !strings.Contains(err.Error(), "already published") {
		t.Errorf("unexpected error: %v", err)
	}

	latest, ok := LatestVersion(out, "IN.TEST")
	if !ok || latest != "1.0.0" {
		t.Errorf("LatestVersion = %q, %v", latest, ok)
	}
}

func TestBuildRequiresAccountability(t *testing.T) {
	dir := writeContent(t)
	g, _ := kg.LoadDir(dir)

	cases := []struct {
		name string
		in   BuildInput
		want string
	}{
		{"no publisher", BuildInput{Dir: dir, Graph: g, Version: "1.0.0",
			EffectiveFrom: value.Date{Year: 2026, Month: 1, Day: 1}}, "published-by"},
		{"no effective date", BuildInput{Dir: dir, Graph: g, Version: "1.0.0",
			PublishedBy: "x"}, "effective-from"},
		{"bad version", BuildInput{Dir: dir, Graph: g, Version: "v1", PublishedBy: "x",
			EffectiveFrom: value.Date{Year: 2026, Month: 1, Day: 1}}, "semantic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.in)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestVersionOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.1", -1},
		{"1.2.0", "1.10.0", -1},
		{"2.0.0", "1.99.99", 1},
		{"1.4.0", "1.4.0", 0},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
