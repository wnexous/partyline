package archtest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// graph maps a package of internal/ to the packages of internal/ that its production code imports,
// all without the "internal/" prefix. Tests are not part of it: what a test imports to build its
// fixtures is not a dependency of the product.
type graph map[string][]string

// layerTable maps a package of internal/ to its layer.
type layerTable map[string]layer

// realLayers is packagesOf as a table. It returns the packages that packagesOf lists in more than one layer.
func realLayers() (layerTable, []string) {
	table, twice := layerTable{}, []string{}
	for l, pkgs := range packagesOf {
		for _, p := range pkgs {
			if _, ok := table[p]; ok {
				twice = append(twice, p)
			}
			table[p] = l
		}
	}
	slices.Sort(twice)
	return table, twice
}

// guidance is what a failure of the boundary test tells whoever met it.
const guidance = `The core (integrations -> per-AI queues -> AI sessions -> sends) must never depend on the app
(rooms, conversations and the messenger pieces): AD-025, in .specs/STATE.md.

If a core package needs something of the app, move that code into the app, or give the core an
interface that the app implements and the wiring (internal/app) registers. Do not add the import to
"allowed": that list only shrinks.`

// check returns what is wrong with the imports of the packages in g, given their layers and the
// imports in allow that the core is still allowed to make:
//
//   - a package without a layer;
//   - a core package that reaches an app package, directly or through packages that are not core, other
//     than through an allowed import. A core package that reaches the app through another core package is
//     not reported: the one whose import it is, is;
//   - an entry of allow that is not needed any more: its import is gone, its importer is not a core
//     package, or what it imports no longer leads to the app. The list only shrinks.
func check(g graph, layers layerTable, allow []edge) []string {
	var problems []string
	pkgs := sortedKeys(g)
	for _, p := range pkgs {
		if _, ok := layers[p]; !ok {
			problems = append(problems, fmt.Sprintf(
				"internal/%s has no layer: add it to packagesOf in internal/archtest/layers_test.go (core, app, kernel or infra)", p))
		}
	}

	allowedImport := map[edge]bool{}
	for _, e := range allow {
		if allowedImport[e] {
			problems = append(problems, fmt.Sprintf("allowed lists internal/%s -> internal/%s twice", e.from, e.to))
		}
		allowedImport[e] = true
	}

	// toApp returns a path from p to an app package through packages that are not core, or nil. A core
	// package ends the search: what it imports is its own business.
	memo := map[string][]string{}
	var toApp func(p string) []string
	toApp = func(p string) []string {
		if path, done := memo[p]; done {
			return path
		}
		memo[p] = nil // an import cycle, which Go does not have, is no path
		var path []string
		switch layers[p] {
		case app:
			path = []string{p}
		case core:
		default:
			for _, d := range sorted(g[p]) {
				if rest := toApp(d); rest != nil {
					path = append([]string{p}, rest...)
					break
				}
			}
		}
		memo[p] = path
		return path
	}

	for _, from := range pkgs {
		if layers[from] != core {
			continue
		}
		for _, to := range sorted(g[from]) {
			if allowedImport[edge{from, to}] {
				continue
			}
			if path := toApp(to); path != nil {
				problems = append(problems, fmt.Sprintf("core package internal/%s reaches the app: %s",
					from, "internal/"+strings.Join(append([]string{from}, path...), " -> internal/")))
			}
		}
	}

	for _, e := range allow {
		why := ""
		switch {
		case layers[e.from] != core:
			why = fmt.Sprintf("internal/%s is not a core package", e.from)
		case !slices.Contains(g[e.from], e.to):
			why = fmt.Sprintf("internal/%s no longer imports internal/%s", e.from, e.to)
		case toApp(e.to) == nil:
			why = fmt.Sprintf("internal/%s no longer leads to the app", e.to)
		}
		if why != "" {
			problems = append(problems, fmt.Sprintf(
				"allowed lists internal/%s -> internal/%s, but %s: remove the entry from allowed and lower maxAllowed", e.from, e.to, why))
		}
	}
	return problems
}

// sorted returns a sorted copy of s.
func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}

// probe finds the import path of this package, whose prefix is the module's.
type probe struct{}

// loadGraph reads the import graph of internal/ with `go list`, once: the go tool resolves the build
// constraints and the import paths, so the graph is what the compiler sees.
var loadGraph = sync.OnceValues(func() (graph, error) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("the go tool is needed to read the import graph: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		if parent := filepath.Dir(root); parent != root {
			root = parent
			continue
		}
		return nil, fmt.Errorf("no go.mod above %s", wd)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, goTool, "list", "-f", `{{.ImportPath}} {{join .Imports " "}}`, "./internal/...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, stderr.String())
	}

	module := strings.TrimSuffix(reflect.TypeOf(probe{}).PkgPath(), "/internal/archtest")
	prefix := module + "/internal/"
	g := graph{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		pkg, ok := strings.CutPrefix(fields[0], prefix)
		if !ok {
			return nil, fmt.Errorf("go list printed %q, which is not under %s", fields[0], prefix)
		}
		g[pkg] = []string{}
		for _, imp := range fields[1:] {
			if dep, ok := strings.CutPrefix(imp, prefix); ok {
				g[pkg] = append(g[pkg], dep)
			}
		}
	}
	return g, nil
})

// AD-025: no package of the core imports a package of the app, directly or through any other package,
// except through the imports that "allowed" still lists. A new violation fails here with the chain of
// imports that leads to the app.
func TestTheCoreDoesNotImportTheApp(t *testing.T) {
	g, err := loadGraph()
	require.NoError(t, err)
	layers, _ := realLayers()

	problems := check(g, layers, allowed)

	if len(problems) > 0 {
		msg := strings.Join(problems, "\n")
		if slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, "reaches the app") }) {
			msg += "\n\n" + guidance
		}
		t.Fatal(msg)
	}
}

// AD-025: the table of layers is the table of the packages: each package of internal/ has exactly one
// layer, and the table names no package that is gone (a rename would otherwise leave a package
// unchecked under its old name).
func TestTheLayerTableMatchesThePackages(t *testing.T) {
	g, err := loadGraph()
	require.NoError(t, err)
	layers, twice := realLayers()

	require.Empty(t, twice, "packages listed in more than one layer in packagesOf")
	var unlisted, gone []string
	for _, p := range sortedKeys(g) {
		if _, ok := layers[p]; !ok {
			unlisted = append(unlisted, p)
		}
	}
	for _, p := range sortedKeys(layers) {
		if _, ok := g[p]; !ok {
			gone = append(gone, p)
		}
	}
	require.Empty(t, unlisted, "packages of internal/ with no layer: add them to packagesOf in layers_test.go")
	require.Empty(t, gone, "packages of packagesOf that do not exist: remove or rename them")
}

// AD-025: the list of known violations only shrinks. maxAllowed follows it down, so that it cannot be
// used later to let in a different violation.
func TestTheAllowListOnlyShrinks(t *testing.T) {
	require.Equal(t, maxAllowed, len(allowed),
		"allowed has %d entries and maxAllowed is %d: lower maxAllowed when you remove an entry, and never raise it (AD-025)",
		len(allowed), maxAllowed)
}

// testLayers is the layers of the synthetic graphs below.
var testLayers = layerTable{
	"conn": core, "queue": core, "agents": core,
	"rooms": app, "delivery": app,
	"access": kernel, "events": kernel,
	"convert": infra, "wiring": infra,
}

// A core that only imports the core, the kernel and the infrastructure, and an app that imports the kernel,
// has nothing to report; the wiring may import both.
func TestCheckAcceptsACoreThatStaysInItsLane(t *testing.T) {
	g := graph{
		"conn": {"access", "queue", "events"}, "queue": {"events"}, "agents": {"queue", "access"},
		"rooms": {"access", "events"}, "delivery": {"rooms"},
		"access": {}, "events": {}, "convert": {"access"}, "wiring": {"conn", "rooms", "agents", "convert"},
	}
	require.Empty(t, check(g, testLayers, nil))
}

// A core package that starts to import an app package is reported with the import.
func TestCheckReportsANewImportOfTheApp(t *testing.T) {
	g := graph{"conn": {"access", "rooms"}, "access": {}, "rooms": {"access"}}

	problems := check(g, testLayers, nil)

	require.Equal(t, []string{"core package internal/conn reaches the app: internal/conn -> internal/rooms"}, problems)
}

// The app can be reached through other packages: a kernel package that grows an import of the app drags every
// core package that uses it, and the report shows the chain.
func TestCheckReportsAnImportThatReachesTheAppThroughAnotherPackage(t *testing.T) {
	g := graph{
		"conn": {"access"}, "queue": {"convert"},
		"access": {"events", "delivery"}, "events": {}, "convert": {"rooms"},
		"delivery": {"rooms"}, "rooms": {},
	}

	problems := check(g, testLayers, nil)

	require.Equal(t, []string{
		"core package internal/conn reaches the app: internal/conn -> internal/access -> internal/delivery",
		"core package internal/queue reaches the app: internal/queue -> internal/convert -> internal/rooms",
	}, problems)
}

// An allowed import is let through, and so is a core package that imports the package that makes it: the
// violation is the allowed import, not each of its importers.
func TestCheckLetsAnAllowedImportThrough(t *testing.T) {
	g := graph{
		"agents": {"rooms", "delivery", "convert", "queue"}, "queue": {}, "conn": {"agents"},
		"rooms": {}, "delivery": {"rooms"}, "convert": {"rooms"},
	}
	allow := []edge{{"agents", "rooms"}, {"agents", "delivery"}, {"agents", "convert"}}

	require.Empty(t, check(g, testLayers, allow))

	// A fourth way to the app is not let through by the other three, and it is agents that is reported for
	// it, not conn, which imports agents.
	g["agents"] = append(g["agents"], "access")
	g["access"] = []string{"delivery"}
	problems := check(g, testLayers, allow)
	require.Equal(t, []string{
		"core package internal/agents reaches the app: internal/agents -> internal/access -> internal/delivery",
	}, problems)
}

// A package that reaches the app through another core package is not reported: the other one is, once, whoever
// imports it.
func TestCheckReportsTheCorePackageThatImportsTheAppAndNotItsImporters(t *testing.T) {
	g := graph{"conn": {"rooms"}, "queue": {"conn"}, "agents": {"queue", "conn"}, "rooms": {}}

	problems := check(g, testLayers, nil)

	require.Equal(t, []string{"core package internal/conn reaches the app: internal/conn -> internal/rooms"}, problems)
}

// When the code of an allowed import is gone, the entry is reported: the list only shrinks.
func TestCheckReportsAnAllowedEntryWhoseImportIsGone(t *testing.T) {
	g := graph{"agents": {"queue"}, "queue": {}, "rooms": {}}

	problems := check(g, testLayers, []edge{{"agents", "rooms"}})

	require.Equal(t, []string{
		"allowed lists internal/agents -> internal/rooms, but internal/agents no longer imports internal/rooms: " +
			"remove the entry from allowed and lower maxAllowed",
	}, problems)
}

// An entry for an import that no longer leads to the app is reported too: it is no violation any more.
func TestCheckReportsAnAllowedEntryThatNoLongerLeadsToTheApp(t *testing.T) {
	g := graph{"agents": {"convert"}, "convert": {"access"}, "access": {}, "rooms": {}}

	problems := check(g, testLayers, []edge{{"agents", "convert"}})

	require.Equal(t, []string{
		"allowed lists internal/agents -> internal/convert, but internal/convert no longer leads to the app: " +
			"remove the entry from allowed and lower maxAllowed",
	}, problems)
}

// The list is for the imports of the core: an entry for another package, or for one that is not known, is
// reported, and so is an entry that is listed twice.
func TestCheckReportsAllowedEntriesThatDoNotBelong(t *testing.T) {
	g := graph{"wiring": {"rooms"}, "rooms": {}, "agents": {"rooms"}}

	problems := check(g, testLayers, []edge{{"wiring", "rooms"}, {"agents", "rooms"}, {"agents", "rooms"}})

	require.Equal(t, []string{
		"allowed lists internal/agents -> internal/rooms twice",
		"allowed lists internal/wiring -> internal/rooms, but internal/wiring is not a core package: " +
			"remove the entry from allowed and lower maxAllowed",
	}, problems)
}

// A package that nobody placed in a layer is reported: it would escape the check.
func TestCheckReportsAPackageWithoutALayer(t *testing.T) {
	g := graph{"newthing": {"rooms"}, "rooms": {}}

	problems := check(g, testLayers, nil)

	require.Equal(t, []string{
		"internal/newthing has no layer: add it to packagesOf in internal/archtest/layers_test.go (core, app, kernel or infra)",
	}, problems)
}
