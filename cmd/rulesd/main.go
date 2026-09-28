// Command rulesd serves the regulatory intelligence engine over HTTP, together
// with the browser console that drives it.
//
// It is a single self-contained binary: the web assets are embedded, there is
// no build step, no package manager and no external CDN. Start it and open the
// address it prints.
//
//	go run ./cmd/rulesd --content content/jurisdictions/IN-TN
//
// A note on the clock. The engine itself never reads one — every evaluation is
// anchored to an explicit date so it can be reproduced (P1, P6). A live service
// still has to decide what "today" means, so that decision is made here, at the
// request boundary, in the jurisdiction's own timezone, and the resulting date
// is passed into the engine explicitly and echoed back on every response. Pass
// --as-of to pin it instead, which is what a reproducible demo wants.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/setu-platform/setu/pkg/compile"
	"github.com/setu-platform/setu/pkg/eval"
	"github.com/setu-platform/setu/pkg/kg"
	"github.com/setu-platform/setu/pkg/sema"
	"github.com/setu-platform/setu/pkg/srl"
	"github.com/setu-platform/setu/pkg/value"
)

//go:embed web
var webFS embed.FS

// server holds the compiled ruleset. It is built once at start-up and is
// immutable thereafter, so every handler can read it concurrently without a
// lock.
type server struct {
	graph    *kg.Graph
	prog     *compile.Program
	engine   *eval.Engine
	layers   *value.Layers
	varTypes map[string]sema.Type

	contentDir string
	pinnedAsOf value.Date // zero means "use the request-time date"
	loc        *time.Location
	scenarios  []scenario
	warnings   []srl.Diagnostic
}

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	content := flag.String("content", "content/jurisdictions/IN-TN", "jurisdiction content directory")
	scenarioDir := flag.String("scenarios", "content/goldens", "directory of example profiles")
	asOf := flag.String("as-of", "", "pin every evaluation to this date (YYYY-MM-DD); "+
		"omit to use the current date in --timezone")
	tz := flag.String("timezone", "Asia/Kolkata", "timezone whose civil date counts as today")
	flag.Parse()

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		log.Fatalf("unknown timezone %q: %v", *tz, err)
	}

	s := &server{contentDir: *content, loc: loc}
	if *asOf != "" {
		d, err := value.ParseDate(*asOf)
		if err != nil {
			log.Fatalf("--as-of: %v", err)
		}
		s.pinnedAsOf = d
	}
	if err := s.load(); err != nil {
		log.Fatalf("loading %s: %v", *content, err)
	}
	s.loadScenarios(*scenarioDir)

	mux := http.NewServeMux()
	s.routes(mux)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", *addr, err)
	}

	s.banner(ln.Addr().String())

	// Shut down cleanly so a restart during development never leaves the port
	// held by a dying process.
	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		fmt.Println("\nshutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		close(idle)
	}()

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-idle
}

// load parses, checks and compiles the jurisdiction's content.
//
// The service refuses to start on any error. Serving determinations from a
// ruleset that does not type-check would mean answering a citizen from content
// nobody has verified is even internally consistent.
func (s *server) load() error {
	g, diags := kg.LoadDir(s.contentDir)
	if g == nil {
		return fmt.Errorf("no content loaded")
	}
	diags = append(diags, sema.Check(g)...)

	prog, cdiags := compile.Compile(g)
	diags = append(diags, cdiags...)

	var errs []string
	for _, d := range diags {
		if d.Severity == srl.SevError {
			errs = append(errs, d.String())
		} else {
			s.warnings = append(s.warnings, d)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d error(s):\n%s", len(errs), strings.Join(errs, "\n"))
	}

	layers, err := kg.LoadLayers(s.contentDir)
	if err != nil {
		return err
	}

	varTypes, _ := sema.VarTypes(g)

	s.graph, s.prog, s.layers, s.varTypes = g, prog, layers, varTypes
	s.engine = eval.New(prog)
	return nil
}

// today is the anchor date for a request.
func (s *server) today() value.Date {
	if !s.pinnedAsOf.IsZero() {
		return s.pinnedAsOf
	}
	n := time.Now().In(s.loc)
	return value.Date{Year: n.Year(), Month: int(n.Month()), Day: n.Day()}
}

func (s *server) routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/ruleset", s.handleRuleset)
	mux.HandleFunc("/api/schema", s.handleSchema)
	mux.HandleFunc("/api/scenarios", s.handleScenarios)
	mux.HandleFunc("/api/evaluate", s.handleEvaluate)
	mux.HandleFunc("/api/whatif", s.handleWhatIf)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
}

func (s *server) banner(addr string) {
	url := "http://" + addr
	if strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "[::1]:") {
		url = "http://localhost:" + addr[strings.LastIndex(addr, ":")+1:]
	}
	line := strings.Repeat("─", 66)
	fmt.Println(line)
	fmt.Println("  SETU — regulatory intelligence engine")
	fmt.Println(line)
	fmt.Printf("  console      %s\n", url)
	fmt.Printf("  jurisdiction %s\n", s.graph.Package)
	fmt.Printf("  content      %s\n", s.contentDir)
	fmt.Printf("  loaded       %s\n", s.graph.CountsString())
	if s.layers != nil {
		fmt.Printf("  layers       %s\n", s.layers.Describe())
	}
	if s.pinnedAsOf.IsZero() {
		fmt.Printf("  anchor date  today in %s (pass --as-of to pin it)\n", s.loc)
	} else {
		fmt.Printf("  anchor date  pinned to %s\n", s.pinnedAsOf)
	}
	if n := len(s.warnings); n > 0 {
		fmt.Printf("  warnings     %d (run `setuctl check` to see them)\n", n)
	}
	fmt.Println(line)
	fmt.Println("  Ctrl-C to stop")
	fmt.Println()
}

// logRequests writes one line per request. It deliberately logs no request
// body: a Project Profile is personal and commercial data, and spec 19 forbids
// personal data in logs.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status,
				time.Since(start).Round(time.Microsecond))
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
