// Command check-openapi-drift compares paths in openapi/openapi.yaml against
// the chi routes registered in internal/transport/router.go and fails (exit 1)
// if either side is missing entries the other declares.
//
// It is intentionally a static parser — it does not import internal/transport
// to avoid pulling in the full dependency graph for a CI check. The matcher
// is line-oriented and tolerant: it normalises chi-style ":foo" path params
// to OpenAPI-style "{foo}", and ignores standard noise (tags, descriptions,
// nested groups).
//
// Usage:
//
//	go run ./tools/check-openapi-drift
//	# exit 0  → in sync
//	# exit 1  → drift detected, report printed to stderr
//	# exit 2  → tool error (cannot read inputs)
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	openapiPath  = "openapi/openapi.yaml"
	routerPath   = "internal/transport/router.go"
	knownGapPath = "tools/check-openapi-drift/known-gaps.txt"
)

func main() {
	specPaths, err := readOpenAPIPaths(openapiPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read openapi: %v\n", err)
		os.Exit(2)
	}
	routerPaths, err := readRouterPaths(routerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read router: %v\n", err)
		os.Exit(2)
	}

	knownSpecOnly, knownRouterOnly, err := readKnownGaps(knownGapPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read known-gaps: %v\n", err)
		os.Exit(2)
	}

	missingFromRouter := filterOut(setDiff(specPaths, routerPaths), knownSpecOnly)
	missingFromSpec := filterOut(setDiff(routerPaths, specPaths), knownRouterOnly)

	if len(missingFromRouter) == 0 && len(missingFromSpec) == 0 {
		fmt.Printf("openapi-drift: OK (%d paths in spec, %d in router, %d known gaps allowed)\n",
			len(specPaths), len(routerPaths), len(knownSpecOnly)+len(knownRouterOnly))
		return
	}

	fmt.Fprintln(os.Stderr, "openapi-drift: NEW DRIFT DETECTED")
	if len(missingFromRouter) > 0 {
		fmt.Fprintln(os.Stderr, "\nIn openapi.yaml but NOT registered in router.go:")
		for _, p := range missingFromRouter {
			fmt.Fprintf(os.Stderr, "  - %s\n", p)
		}
	}
	if len(missingFromSpec) > 0 {
		fmt.Fprintln(os.Stderr, "\nRegistered in router.go but NOT documented in openapi.yaml:")
		for _, p := range missingFromSpec {
			fmt.Fprintf(os.Stderr, "  + %s\n", p)
		}
	}
	fmt.Fprintf(os.Stderr, "\nFix: either update openapi.yaml / router.go, or whitelist the entry in %s\n", knownGapPath)
	os.Exit(1)
}

// readKnownGaps loads the whitelist of drift entries we're choosing to live
// with (typically: in-flight migrations to spec parity). Format is one path
// per line, prefixed by `+ ` (in router but not spec) or `- ` (in spec but
// not router). Blank lines and `#`-prefixed comments are ignored.
func readKnownGaps(path string) (specOnly map[string]struct{}, routerOnly map[string]struct{}, err error) {
	specOnly = map[string]struct{}{}
	routerOnly = map[string]struct{}{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return specOnly, routerOnly, nil
		}
		return nil, nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "- "):
			specOnly[strings.TrimSpace(line[2:])] = struct{}{}
		case strings.HasPrefix(line, "+ "):
			routerOnly[strings.TrimSpace(line[2:])] = struct{}{}
		}
	}
	return specOnly, routerOnly, scanner.Err()
}

func filterOut(paths []string, exclude map[string]struct{}) []string {
	var out []string
	for _, p := range paths {
		if _, ok := exclude[p]; ok {
			continue
		}
		out = append(out, p)
	}
	return out
}

// readOpenAPIPaths returns the set of paths declared under the `paths:`
// section of an OpenAPI 3 YAML. It is a deliberately shallow parser — it
// looks for two-space-indented top-level path keys after a `paths:` line
// and stops at the next top-level section.
func readOpenAPIPaths(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	pathLine := regexp.MustCompile(`^  (\/[^:]+):\s*$`)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	inPaths := false
	out := map[string]struct{}{}
	for scanner.Scan() {
		line := scanner.Text()
		if !inPaths {
			if strings.HasPrefix(line, "paths:") {
				inPaths = true
			}
			continue
		}
		// stop when we leave the paths block (zero-indent, non-empty line)
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break
		}
		if m := pathLine.FindStringSubmatch(line); m != nil {
			out[normalisePath(m[1])] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sortedKeys(out), nil
}

// readRouterPaths returns the set of HTTP paths registered through chi.
// It matches lines containing chi method calls (.Get/.Post/.Put/.Patch/.Delete
// /.Head/.Options) and extracts the first quoted argument as the path.
//
// chi routers are nested via .Route("/prefix", func(r) { ... }); we keep
// a stack of active prefixes by tracking opening/closing braces inside a
// chi.Router scope. This is a heuristic — false positives are far better
// than false negatives for a drift check.
func readRouterPaths(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	methodCall := regexp.MustCompile(`\.(Get|Post|Put|Patch|Delete|Head|Options)\("([^"]+)"`)
	routeCall := regexp.MustCompile(`\.(Route|Mount)\("([^"]+)"`)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	type frame struct {
		prefix string
		depth  int // brace depth at the time this frame was pushed
	}
	stack := []frame{{prefix: "", depth: 0}}
	depth := 0
	out := map[string]struct{}{}

	for scanner.Scan() {
		raw := scanner.Text()
		// Skip comments to avoid matching example code
		stripped := stripLineComment(raw)
		opens := strings.Count(stripped, "{")
		closes := strings.Count(stripped, "}")

		if m := routeCall.FindStringSubmatch(stripped); m != nil {
			parent := stack[len(stack)-1].prefix
			stack = append(stack, frame{prefix: joinPath(parent, m[2]), depth: depth})
		}

		if m := methodCall.FindStringSubmatch(stripped); m != nil {
			parent := stack[len(stack)-1].prefix
			full := joinPath(parent, m[2])
			out[normalisePath(full)] = struct{}{}
		}

		depth += opens - closes
		// Pop any frames that closed in this line.
		for len(stack) > 1 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sortedKeys(out), nil
}

// joinPath concatenates two path segments, collapsing double slashes and
// special-casing the trailing "/" used by chi for "exact prefix" routes.
func joinPath(prefix, suffix string) string {
	if suffix == "/" && prefix != "" {
		return prefix
	}
	combined := prefix + suffix
	combined = strings.ReplaceAll(combined, "//", "/")
	return combined
}

// normalisePath canonicalises:
//  1. chi style ":foo" path params → OpenAPI style "{foo}"
//  2. trailing slashes are stripped (except for root "/") so "/x" == "/x/"
func normalisePath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if strings.HasPrefix(seg, ":") {
			parts[i] = "{" + seg[1:] + "}"
		}
	}
	out := strings.Join(parts, "/")
	if len(out) > 1 && strings.HasSuffix(out, "/") {
		out = strings.TrimRight(out, "/")
	}
	return out
}

func stripLineComment(s string) string {
	if i := strings.Index(s, "//"); i >= 0 {
		// Don't strip inside a string literal — best-effort: only strip if
		// // is preceded by whitespace or start of line.
		if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
			return s[:i]
		}
	}
	return s
}

func setDiff(a, b []string) []string {
	bset := map[string]struct{}{}
	for _, x := range b {
		bset[x] = struct{}{}
	}
	var out []string
	for _, x := range a {
		if _, ok := bset[x]; !ok {
			out = append(out, x)
		}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
