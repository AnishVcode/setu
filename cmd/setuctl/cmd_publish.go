package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/setu-platform/setu/pkg/ruleset"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/testkit"
)

func runPublish(args []string) error {
	fs := flagSet("publish")
	version := fs.String("version", "", "semantic version to publish, e.g. 1.4.0 (required)")
	effective := fs.String("effective-from", "", "date this version takes effect (YYYY-MM-DD, required)")
	publishedAt := fs.String("published-at", "", "publication timestamp to record (required)")
	publishedBy := fs.String("published-by", "", "the accountable role publishing this version (required)")
	note := fs.String("note", "", "publication note, shown in the version history")
	production := fs.Bool("production", false, "apply the production gate: unverified or unowned content is rejected")
	out := fs.String("out", "dist/rulesets", "directory to write the published version into")
	keyPath := fs.String("key", "", "path to an Ed25519 private key to sign the snapshot with")
	signer := fs.String("signer", "", "name recorded alongside the signature")
	langs := fs.String("languages", "en", "comma-separated languages that must be complete")
	today := fs.String("as-of", "", "date to evaluate staleness and run tests against (YYYY-MM-DD, required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir := contentDir(fs, "content/jurisdictions/IN-TN")

	if *version == "" {
		return fmt.Errorf("--version is required")
	}
	anchor, err := parseAsOf(*today)
	if err != nil {
		return err
	}
	effectiveFrom, err := parseDateFlag("--effective-from", *effective)
	if err != nil {
		return err
	}
	if *publishedAt == "" {
		return fmt.Errorf("--published-at is required: the toolchain never reads the clock, " +
			"so that a publish is reproducible from its inputs alone")
	}

	fmt.Printf("Publishing %s version %s\n", dir, *version)
	fmt.Println(strings.Repeat("─", 78))

	// ---- Gate 1: the content must be clean. ----
	l, err := load(dir, loadOptions{
		Lint: true,
		LintCfg: sema.LintConfig{
			Languages:       splitCSV(*langs),
			RequireVerified: *production,
			Today:           anchor,
		},
	})
	if err != nil {
		return err
	}
	if n := l.Errors(); n > 0 {
		printDiagnostics(l, false)
		if *production {
			return fmt.Errorf("%d error(s) under the production gate; "+
				"content that has not been through legal review and department sign-off "+
				"cannot be published to a production jurisdiction", n)
		}
		return fmt.Errorf("%d error(s); publication refused", n)
	}
	fmt.Printf("  gate 1  content check ......... pass (%d warning(s))\n", l.Warnings())

	// ---- Gate 2: every authored test must pass. ----
	rep := testkit.RunRuleTests(l.Prog, anchor, l.Layers)
	if rep.Failed > 0 {
		for _, f := range rep.Failures() {
			fmt.Printf("    FAIL %s / %s: expected %s, got %s\n",
				f.RuleID, f.TestName, f.Expected, f.Actual)
		}
		return fmt.Errorf("%d authored rule test(s) failed; publication refused", rep.Failed)
	}
	fmt.Printf("  gate 2  authored rule tests ... pass (%d cases)\n", rep.Passed)

	// ---- Gate 3: every rule must have positive and negative coverage. ----
	gaps := testkit.CoverageGaps(l.Prog)
	if len(gaps) > 0 {
		for _, g := range gaps {
			fmt.Println("    " + g)
		}
		return fmt.Errorf("%d rule(s) lack the required test coverage; publication refused", len(gaps))
	}
	fmt.Printf("  gate 3  test coverage ......... pass (every rule has both cases)\n")

	// ---- Gate 4: the property suite must be clean. ----
	pcfg := testkit.DefaultPropertyConfig(anchor)
	pcfg.Layers = l.Layers
	prep := testkit.RunProperties(l.Prog, pcfg)
	if len(prep.Violations) > 0 {
		for _, v := range prep.Violations {
			fmt.Printf("    VIOLATION [%s] %s\n", v.Property, v.Detail)
		}
		return fmt.Errorf("%d property violation(s); publication refused", len(prep.Violations))
	}
	fmt.Printf("  gate 4  property suite ........ pass (%d generated profiles)\n", prep.Cases)

	// ---- Gate 5: the version must move forward. ----
	if prev, ok := ruleset.LatestVersion(*out, l.Graph.Package); ok {
		if ruleset.CompareVersions(*version, prev) <= 0 {
			return fmt.Errorf("version %s does not exceed the published version %s; "+
				"published versions are immutable and the sequence only moves forward",
				*version, prev)
		}
		fmt.Printf("  gate 5  version sequence ...... pass (%s → %s)\n", prev, *version)
	} else {
		fmt.Printf("  gate 5  version sequence ...... pass (first version)\n")
	}

	// ---- Build, sign and write. ----
	snap, err := ruleset.Build(ruleset.BuildInput{
		Dir: dir, Graph: l.Graph, Layers: l.Layers,
		Version: *version, EffectiveFrom: effectiveFrom,
		PublishedAt: *publishedAt, PublishedBy: *publishedBy,
		Note: *note, Production: *production, Today: anchor,
	})
	if err != nil {
		return err
	}

	if *keyPath != "" {
		key, err := loadPrivateKey(*keyPath)
		if err != nil {
			return err
		}
		name := *signer
		if name == "" {
			name = *publishedBy
		}
		if err := snap.Sign(key, name); err != nil {
			return err
		}
		fmt.Printf("  signed by %s\n", name)
	} else {
		fmt.Println("  NOT SIGNED — a production ruleset must be signed with an HSM-backed key")
	}

	path, err := snap.Write(*out)
	if err != nil {
		return err
	}

	fmt.Println(strings.Repeat("─", 78))
	fmt.Printf("published %s %s\n", snap.Package, snap.Version)
	fmt.Printf("  effective from  %s\n", snap.EffectiveFrom)
	fmt.Printf("  content hash    %s\n", snap.ContentHash)
	fmt.Printf("  files           %d\n", len(snap.Files))
	fmt.Printf("  contents        %s\n", joinSorted(snap.Counts))
	fmt.Printf("  written to      %s\n", path)
	if len(snap.Governance.DeemedApprovals) > 0 {
		fmt.Printf("\n  deemed approval is permitted for %d approval(s) in this version:\n",
			len(snap.Governance.DeemedApprovals))
		for _, d := range snap.Governance.DeemedApprovals {
			fmt.Println("    " + d)
		}
	}
	if snap.Governance.RulesUnverified > 0 {
		fmt.Printf("\n  WARNING: %d rule(s) in this version are unverified. "+
			"This snapshot must not be served to applicants.\n", snap.Governance.RulesUnverified)
	}
	return nil
}

func runVerify(args []string) error {
	fs := flagSet("verify")
	pubPath := fs.String("public-key", "", "path to the Ed25519 public key to verify the signature with")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("give the path to a snapshot.json")
	}
	snap, err := ruleset.Load(fs.Arg(0))
	if err != nil {
		return err
	}

	var pub ed25519.PublicKey
	if *pubPath != "" {
		pub, err = loadPublicKey(*pubPath)
		if err != nil {
			return err
		}
	}

	res := snap.Verify(pub)
	fmt.Printf("%s version %s\n", snap.Package, snap.Version)
	fmt.Printf("  published %s by %s\n", snap.PublishedAt, snap.PublishedBy)
	fmt.Printf("  effective from %s\n", snap.EffectiveFrom)
	fmt.Printf("  content hash   %s\n", snap.ContentHash)
	fmt.Printf("  recomputed     %s\n", res.ComputedHash)

	if len(res.FileMismatches) > 0 {
		fmt.Printf("  FILE TAMPERING: %s\n", strings.Join(res.FileMismatches, ", "))
	} else {
		fmt.Printf("  files          %d, all digests match\n", len(snap.Files))
	}
	switch {
	case !res.SignatureChecked && snap.Signature == "":
		fmt.Println("  signature      absent")
	case !res.SignatureChecked:
		fmt.Println("  signature      present but not checked (no --public-key given)")
	case res.SignatureValid:
		fmt.Printf("  signature      valid, by %s\n", snap.SignedBy)
	default:
		fmt.Println("  signature      INVALID")
	}

	// Re-parsing the embedded content proves the snapshot is self-sufficient:
	// a case pinned to this version can be re-evaluated from the snapshot alone.
	g, diags := snap.Graph()
	errs := 0
	for _, d := range diags {
		if d.Severity == 0 {
			errs++
		}
	}
	if errs > 0 {
		fmt.Printf("  embedded content does not re-parse cleanly: %d error(s)\n", errs)
		return fmt.Errorf("verification failed")
	}
	fmt.Printf("  embedded content re-parses: %s\n", g.CountsString())

	if !res.OK() {
		return fmt.Errorf("verification failed")
	}
	fmt.Println("\nsnapshot verified")
	return nil
}

func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("%s: expected a hex-encoded Ed25519 private key: %w", path, err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s: key is %d bytes, expected %d", path, len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

func loadPublicKey(path string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("%s: expected a hex-encoded Ed25519 public key: %w", path, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s: key is %d bytes, expected %d", path, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}
