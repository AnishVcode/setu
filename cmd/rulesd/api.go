package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/setu-platform/setu/pkg/discovery"
	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/unit"
	"github.com/setu-platform/setu/pkg/value"
)

const maxBody = 1 << 20 // 1 MB; a profile is small and an oversized body is abuse

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		// The status line is already written; there is nothing useful left to
		// say to the client, so record it for the operator instead.
		fmt.Fprintf(os.Stderr, "encoding response: %v\n", err)
	}
}

// apiError is the one error shape every endpoint returns. The message is meant
// to be shown to the person using the console, so it says what was wrong and
// what would fix it rather than naming an internal condition.
type apiError struct {
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
}

func writeErr(w http.ResponseWriter, code int, msg, hint string) {
	writeJSON(w, code, apiError{Error: msg, Hint: hint})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "this endpoint takes a POST", "")
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "could not read the request body", "")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeErr(w, http.StatusBadRequest, "the request body is not valid JSON: "+err.Error(), "")
		return false
	}
	return true
}

// ---------- health and ruleset ----------

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"package": s.graph.Package,
		"asOf":    s.today().String(),
	})
}

func (s *server) handleRuleset(w http.ResponseWriter, r *http.Request) {
	type govSummary struct {
		Verified    int      `json:"verified"`
		InReview    int      `json:"inReview"`
		Unverified  int      `json:"unverified"`
		Deprecated  int      `json:"deprecated"`
		DeemedAllow []string `json:"deemedApprovalPermitted"`
	}
	var gov govSummary
	for _, id := range s.graph.RuleIDs() {
		switch s.graph.Rules[id].Governance.ReviewStatus {
		case srl.StatusVerified:
			gov.Verified++
		case srl.StatusInReview:
			gov.InReview++
		case srl.StatusDeprecated:
			gov.Deprecated++
		default:
			gov.Unverified++
		}
	}
	for _, id := range s.graph.ApprovalIDs() {
		if a := s.graph.Approvals[id]; a.Deemed.Permitted {
			gov.DeemedAllow = append(gov.DeemedAllow, id+" ("+a.Deemed.Section+")")
		}
	}

	layersVersion := ""
	if s.layers != nil {
		layersVersion = s.layers.Version
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"package":       s.graph.Package,
		"meta":          s.graph.Meta,
		"counts":        s.graph.Counts(),
		"layersVersion": layersVersion,
		"asOf":          s.today().String(),
		"asOfPinned":    !s.pinnedAsOf.IsZero(),
		"governance":    gov,
		"warnings":      len(s.warnings),
	})
}

// ---------- schema ----------

// schemaVar describes one profile question to the console.
//
// The form is generated from this, not hardcoded. That is the point: the
// questions an applicant is asked come from the encoded law, so adding a rule
// that reads a new variable puts the question on the form with the wording its
// author wrote, in every language they wrote it in.
type schemaVar struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	Widget    string            `json:"widget"`
	Dimension string            `json:"dimension,omitempty"`
	Unit      string            `json:"unit,omitempty"`
	UnitLabel string            `json:"unitLabel,omitempty"`
	EnumType  string            `json:"enumType,omitempty"`
	Group     map[string]string `json:"group,omitempty"`
	Question  map[string]string `json:"question"`
	Hint      map[string]string `json:"hint,omitempty"`
	Example   string            `json:"example,omitempty"`
	Sensitive bool              `json:"sensitive,omitempty"`
	// ReadBy names the rules that consult this answer, so the console can show
	// a person what a question is actually for.
	ReadBy []string `json:"readBy"`
}

type enumMember struct {
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
}

func (s *server) handleSchema(w http.ResponseWriter, r *http.Request) {
	readBy := map[string][]string{}
	for _, rid := range s.graph.RuleIDs() {
		for _, v := range s.prog.RuleReadsVars[rid] {
			readBy[v] = append(readBy[v], rid)
		}
	}
	for _, fid := range s.graph.FeeIDs() {
		srl.Walk(s.graph.Fees[fid].Amount, func(e srl.Expr) {
			if p, ok := e.(*srl.ProfileRef); ok {
				readBy[p.Var] = append(readBy[p.Var], fid)
			}
		})
	}
	for _, iid := range s.graph.IncentiveIDs() {
		srl.Walk(s.graph.Incentives[iid].Value, func(e srl.Expr) {
			if p, ok := e.(*srl.ProfileRef); ok {
				readBy[p.Var] = append(readBy[p.Var], iid)
			}
		})
	}
	for k := range readBy {
		sort.Strings(readBy[k])
		readBy[k] = dedupe(readBy[k])
	}

	var vars []schemaVar
	for _, name := range s.graph.VarNames() { // declaration order = asking order
		decl := s.graph.Vars[name]
		t := s.varTypes[name]

		sv := schemaVar{
			Name:      name,
			Type:      t.String(),
			Group:     map[string]string(decl.Group),
			Question:  map[string]string(decl.Question),
			Hint:      map[string]string(decl.Hint),
			Example:   decl.Example,
			Sensitive: decl.Sensitive,
			ReadBy:    readBy[name],
		}
		if sv.ReadBy == nil {
			sv.ReadBy = []string{}
		}

		switch t.Kind {
		case sema.Bool:
			sv.Widget = "bool"
		case sema.Number:
			sv.Dimension = t.Dim.String()
			if t.Dim == unit.DimMoney {
				sv.Widget = "money"
			} else {
				sv.Widget = "quantity"
				// The unit the profile declares is the unit a bare number is
				// read in, so the form must show the same one.
				sv.Unit = decl.Unit
				if sv.Unit == "" {
					sv.Unit = unit.Base(t.Dim).Symbol
				}
				if u, ok := unit.Lookup(sv.Unit); ok {
					sv.UnitLabel = u.Label()
				}
			}
		case sema.Text:
			sv.Widget = "text"
		case sema.Date:
			sv.Widget = "date"
		case sema.EnumT:
			sv.Widget = "enum"
			sv.EnumType = t.Name
		case sema.Geo:
			sv.Widget = "geo"
		default:
			sv.Widget = "text"
		}
		vars = append(vars, sv)
	}

	enums := map[string][]enumMember{}
	for _, name := range s.graph.EnumNames() {
		e := s.graph.Enums[name]
		out := make([]enumMember, 0, len(e.Members))
		for _, m := range e.Members {
			out = append(out, enumMember{Name: m.Name, Title: m.Title.Get("en")})
		}
		enums[name] = out
	}

	var layerNames []string
	if s.layers != nil {
		layerNames = s.layers.LayerNames()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"package":   s.graph.Package,
		"variables": vars,
		"enums":     enums,
		"layers":    layerNames,
		"languages": s.languages(),
	})
}

// languages reports every language the content actually carries text in.
func (s *server) languages() []string {
	seen := map[string]bool{}
	for _, name := range s.graph.VarNames() {
		for _, l := range s.graph.Vars[name].Question.Langs() {
			seen[l] = true
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// ---------- scenarios ----------

type scenario struct {
	Key     string         `json:"key"`
	Label   string         `json:"label"`
	Profile map[string]any `json:"profile"`
}

// loadScenarios reads the example profiles that seed the console. They are
// clearly labelled as examples; nothing here is presented as a real applicant's
// data.
func (s *server) loadScenarios(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		key := strings.TrimSuffix(n, ".json")
		s.scenarios = append(s.scenarios, scenario{
			Key: key, Label: prettyLabel(key), Profile: m,
		})
	}
}

func prettyLabel(key string) string {
	parts := strings.FieldsFunc(key, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func (s *server) handleScenarios(w http.ResponseWriter, r *http.Request) {
	out := s.scenarios
	if out == nil {
		out = []scenario{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"scenarios": out})
}

// ---------- evaluate ----------

type evaluateRequest struct {
	Profile  map[string]any `json:"profile"`
	AsOf     string         `json:"asOf,omitempty"`
	Language string         `json:"language,omitempty"`
	Trace    bool           `json:"trace,omitempty"`
}

func (s *server) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	var req evaluateRequest
	if !decodeBody(w, r, &req) {
		return
	}

	asOf, err := s.resolveAsOf(req.AsOf)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "dates are written YYYY-MM-DD")
		return
	}

	profile, err := s.parseProfile(req.Profile)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	lang := req.Language
	if lang == "" {
		lang = "en"
	}

	d := s.engine.Evaluate(
		eval.Input{Profile: profile, AsOf: asOf, Language: lang, Layers: s.layers},
		eval.Options{Trace: req.Trace, RulesetVersion: s.versionLabel()},
	)

	// The determination is the engine's answer and is sent unchanged, so this
	// endpoint and the CLI agree exactly. Everything alongside it is
	// presentation: names for identifiers, and the plain-language account of
	// why each approval applies.
	writeJSON(w, http.StatusOK, map[string]any{
		"determination": d,
		"explanations":  s.buildExplanations(d, profile, lang),
		"titles":        s.buildTitles(lang),
		"departments":   s.departmentsInvolved(d, lang),
	})
}

// versionLabel names the ruleset a determination came from. Serving from a
// working directory rather than a published snapshot is stated plainly rather
// than dressed up as a version number.
func (s *server) versionLabel() string {
	return "working-copy (" + s.contentDir + ")"
}

func (s *server) resolveAsOf(in string) (value.Date, error) {
	if in == "" {
		return s.today(), nil
	}
	if !s.pinnedAsOf.IsZero() {
		// The operator pinned the date; a request may not move it.
		return s.pinnedAsOf, nil
	}
	return value.ParseDate(in)
}

func (s *server) parseProfile(raw map[string]any) (*eval.Profile, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	return eval.ParseProfileJSON(s.prog, b)
}

// ---------- what-if ----------

type whatIfRequest struct {
	Scenarios []struct {
		Name    string         `json:"name"`
		Profile map[string]any `json:"profile"`
	} `json:"scenarios"`
	AsOf     string `json:"asOf,omitempty"`
	Language string `json:"language,omitempty"`
}

func (s *server) handleWhatIf(w http.ResponseWriter, r *http.Request) {
	var req whatIfRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.Scenarios) < 2 {
		writeErr(w, http.StatusBadRequest,
			"a comparison needs at least two scenarios", "")
		return
	}

	asOf, err := s.resolveAsOf(req.AsOf)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), "dates are written YYYY-MM-DD")
		return
	}
	lang := req.Language
	if lang == "" {
		lang = "en"
	}

	var named []discovery.NamedProfile
	for i, sc := range req.Scenarios {
		p, err := s.parseProfile(sc.Profile)
		if err != nil {
			writeErr(w, http.StatusBadRequest,
				fmt.Sprintf("scenario %d (%s): %v", i+1, sc.Name, err), "")
			return
		}
		name := sc.Name
		if name == "" {
			name = fmt.Sprintf("Scenario %d", i+1)
		}
		p.ID = name
		named = append(named, discovery.NamedProfile{Name: name, Profile: p})
	}

	cmp := discovery.Compare(s.engine,
		eval.Input{AsOf: asOf, Language: lang, Layers: s.layers},
		eval.Options{RulesetVersion: s.versionLabel()},
		named)

	// The Comparison holds whole determinations; the console only needs the
	// summary rows and the differences, so send those.
	type row struct {
		ID       string   `json:"id"`
		Title    string   `json:"title"`
		Statuses []string `json:"statuses"`
		Differs  bool     `json:"differs"`
	}
	rows := make([]row, 0, len(cmp.Rows))
	for _, rr := range cmp.Rows {
		st := make([]string, 0, len(rr.Statuses))
		for _, x := range rr.Statuses {
			st = append(st, x.Text())
		}
		rows = append(rows, row{ID: rr.ID, Title: rr.Title, Statuses: st, Differs: rr.Differs})
	}

	cheapest, _ := cmp.Cheapest()
	fastest, _ := cmp.Fastest()
	simplest, _ := cmp.Simplest()

	writeJSON(w, http.StatusOK, map[string]any{
		"asOf":        asOf.String(),
		"summary":     cmp.Summary,
		"rows":        rows,
		"differences": cmp.Differences,
		"cheapest":    cheapest,
		"fastest":     fastest,
		"simplest":    simplest,
	})
}
