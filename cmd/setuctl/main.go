// Command setuctl is the regulatory content toolchain: the command-line half of
// Rule Studio.
//
// It is what a regulatory content officer, and CI, use to check encoded law,
// run its tests, see what it says about a given applicant, compare scenarios,
// and publish an immutable ruleset version.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

const usage = `setuctl — SETU regulatory content toolchain

Usage:
  setuctl <command> [flags]

Commands:
  check     Parse, type-check and lint a jurisdiction's content
  analyze   Search for unreachable rules and contradictory rule pairs
  test      Run authored rule tests and the property-based suite
  eval      Evaluate a profile and print the determination
  explain   Show the full derivation for one approval
  whatif    Compare two or more scenarios side by side
  publish   Gate and emit an immutable ruleset version
  verify    Verify a published ruleset snapshot against its hash
  keygen    Generate a development signing key pair
  version   Print the toolchain version

Run "setuctl <command> -h" for the flags of a command.
`

// Version is the toolchain version, stamped at build time.
var Version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "check":
		err = runCheck(args)
	case "analyze", "analyse":
		err = runAnalyze(args)
	case "test":
		err = runTest(args)
	case "eval":
		err = runEval(args)
	case "explain":
		err = runExplain(args)
	case "whatif":
		err = runWhatIf(args)
	case "publish":
		err = runPublish(args)
	case "verify":
		err = runVerify(args)
	case "keygen":
		err = runKeygen(args)
	case "version":
		fmt.Println("setuctl " + Version)
		return
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "setuctl "+cmd+": "+err.Error())
		os.Exit(1)
	}
}

// flagSet builds a flag set that prints the command's own usage.
func flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("setuctl "+name, flag.ExitOnError)
	return fs
}

// contentDir resolves the positional content directory argument.
func contentDir(fs *flag.FlagSet, def string) string {
	if fs.NArg() > 0 {
		return fs.Arg(0)
	}
	return def
}

func joinSorted(m map[string]int) string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
