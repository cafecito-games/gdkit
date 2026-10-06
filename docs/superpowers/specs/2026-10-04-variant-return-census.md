# The `-> Variant` return census

A measurement, not a design. [Issue #37](https://github.com/cafecito-games/gdkit/issues/37)
asks for counts from real GDScript before anything is built, and this document
is those counts. Nothing here is implemented; the recommendation at the end is
a recommendation.

## 1. The question

`require-return-type`, added by #35, reports a function with no `->`
annotation. It does not report a function written `-> Variant`. The claim to be
tested is that the two are the same hole: the engine checks nothing at the call
site either way, so `-> Variant` satisfies the rule while making a deliberate
declaration out of what is usually a conversion artifact.

Whether that is worth reporting depends on which of three shapes a real
`-> Variant` turns out to be.

| Shape | What a rule should do |
| --- | --- |
| A genuine union return, where `Variant` is the honest type | stay quiet; reporting it is noise |
| A placeholder a conversion left behind | report it; this is the whole point |
| A signature forced by an override, where the base declares `Variant` | stay quiet, but it cannot know that without the base class |

**The override count decides the design.** Deciding that a `-> Variant` is
forced means resolving the overridden method, which means walking the
inheritance graph. `lint` is single-file by construction: a rule sees one
`project.Script` and nothing else. The package that holds an inheritance index
is `generate`, which builds one precisely because `equals` composes with an
ancestor's implementation. So if the override shape is common, this is either a
`generate`-shaped feature or a lint rule shipping with a documented
false-positive class — and if it is absent, a single-file rule is sound as it
stands.

## 2. Method

### Corpus

Three projects on this machine, read-only.

| Project | Root | `.gd` files | Lines |
| --- | --- | --- | --- |
| uzir client | `/Users/christian/CafecitoGames/uzir/client` | 2539 | 461,446 |
| aseprite-importer | `/Users/christian/CafecitoGames/aseprite-importer` | 79 | 6,482 |
| BaristaScript | `/Users/christian/CafecitoGames/BaristaScript/project` | 8 | 8,074 |

One codebase family dominates: uzir is 96.7% of the files (2539 of 2626) and
96.9% of the lines (461,446 of 476,002). Every ratio below is effectively a statement about uzir with two small
checks beside it. That is the single largest limit of this census and section 6
returns to it.

### The excluded fourth project

`/Users/christian/CafecitoGames/uzir-issue-858-prototype/client` holds 1217
`.gd` files and was excluded as a duplicate of the uzir client. The check and
its result:

- Both directories are checkouts of `git@github.com:cafecito-games/uzir.git`.
  The prototype's `HEAD` is `a4b2972eb docs(chat): prototype Laya moderation
  desk (#858)`; the primary corpus is at `2e99d75ed`.
- 1165 of the prototype's 1217 script paths also exist in `uzir/client`
  (95.7%). Of those 1165, 243 are byte-identical and 922 differ, which is what
  an older snapshot of the same tree looks like. The 52 prototype-only paths
  are files since renamed or removed, such as
  `features/game/state/party_state_store.gd`.
- Running the census program over the prototype returns **the same figures as
  the uzir client's authored bucket**: 16 functions with no annotation, 22
  `-> Variant`, 20 of them leading-underscore, 2 confirmed virtuals, 0
  overrides, across the same 18 distinct names. Including it would have doubled
  every number in section 3 without adding one new declaration.

It is a duplicate. It is excluded.

### Separating first-party from vendored code

An `addons/` directory heuristic was tried first and gets two things wrong, so
the program classifies a file three ways instead.

- **vendored** — any path with an `addons` segment. In uzir these 96 files are
  the gpm-installed addons (`vest`, `AuthenticationKit`, `MobileKit`,
  `PurchaseKit`, `SwiftGodot`, `sentry`, `editor_extensions`), every one of
  them listed in `.gitignore`.
- **generated** — untracked by git and outside `addons/`. This is entirely
  uzir's `protocol/` tree: 688 files and 224,485 lines of generated protocol
  code, which is also excluded by uzir's own `.gdkit/lint.json`. Counting it as
  first-party would have put 224,485 lines of machine output into a measurement
  of human typing habits; before the split it was inflating uzir's
  "first-party" line count by 99%.
- **authored** — everything else, meaning code the project's own repository
  tracks.

Two consequences of that classifier are stated rather than hidden.

- An addon repository tracks its own product under `addons/<name>`, so
  aseprite-importer's 12 `spritesheetology` files land in the vendored bucket
  although they are that repository's first-party code. Measured separately
  they hold 64 function declarations, 0 with no annotation and 0 `-> Variant`,
  so they change no `Variant` figure. The corrected authored totals in section
  3 add them back.
- `aseprite-importer` installs `vest` into `tests/godot/addons/vest`, the same
  addon uzir installs — 52 files in both, 9 byte-identical and 43 differing, so
  two releases of one addon. The vendored totals therefore count `vest` twice.
  Section 3 gives a de-duplicated row.

### The program

Thrown away after the run; it is not shipping code and lives nowhere in this
repository. It parses with `gdparser` and walks the tree — no regex over source
text, the same invariant the analyzer holds — and reports a file that failed to
parse as its own count, because a file that did not parse is not a file with no
`Variant` returns.

What it counts, per project and per origin: files, parse failures, lines,
function declarations, declarations with no `->` at all, declarations written
`-> Variant`, and within those the leading-underscore names, the names matching
a confirmed Godot virtual that returns `Variant`, the ones declared in an inner
class, and the ones whose method name is also declared by a project ancestor.
Lambdas are counted separately and are not part of any `-> Variant` figure.

The override index is crude and section 6 says how. It resolves a base through
a `class_name` map, a `res://` or relative string-literal path, and a
`const Name = preload(...)` alias; it reads the base out of the AST rather than
rendered text; it indexes only each file's outermost class; and it reports
separately when the walk stopped at a base it could not resolve, so an
"override: none" answer is distinguishable from "could not tell".

Reproduce with:

```sh
go run . -out result.json \
  uzir=/Users/christian/CafecitoGames/uzir/client \
  aseprite-importer=/Users/christian/CafecitoGames/aseprite-importer \
  baristascript=/Users/christian/CafecitoGames/BaristaScript/project
```

`main.go`, in full:

```go
// Command variantcensus counts function return-type annotations across one or
// more Godot projects, classifying every "-> Variant" declaration by name
// shape and by whether it plausibly overrides a base class declared inside the
// same project.
//
// Usage: variantcensus [-out result.json] name=root [name=root ...]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cafecito-games/gdparser"
	"github.com/cafecito-games/gdparser/ast"
)

// confirmedVariantVirtuals are Godot 4 virtual methods the engine class
// reference documents as returning Variant. The list is documentation
// knowledge, not a measurement, and matching is by name alone: a project
// method that happens to share one of these names is counted here even if its
// class does not inherit the type declaring the virtual.
var confirmedVariantVirtuals = map[string]string{
	"_get":                  "Object._get",
	"_property_get_revert":  "Object._property_get_revert",
	"_iter_get":             "GDScript custom iterator _iter_get",
	"_get_drag_data":        "Control._get_drag_data",
	"_load":                 "ResourceFormatLoader._load",
	"_instantiate":          "ScriptLanguageExtension._instantiate",
	"_call":                 "ScriptLanguageExtension-family _call",
	"_get_default_property": "ScriptExtension._get_default_property",
}

// origin is where a file's text came from. The three populations have
// different typing habits, so a total that mixes them answers nothing.
type origin int

const (
	// authored is code the project's own repository tracks.
	authored origin = iota
	// generated is untracked code outside any addons directory, which in this
	// corpus is protocol code emitted by a generator.
	generated
	// vendored is an installed third-party addon.
	vendored
)

func (value origin) String() string {
	switch value {
	case authored:
		return "authored"
	case generated:
		return "generated"
	default:
		return "vendored"
	}
}

var origins = []origin{authored, generated, vendored}

// classRecord is one indexed class. Only a file's outermost class is indexed;
// an inner class is counted but takes no part in override resolution.
type classRecord struct {
	path           string
	className      string
	hasClassName   bool
	extendsName    string
	extendsPath    string
	extendsChain   []string
	preloadAliases map[string]string
	methods        map[string]bool
	origin         origin
}

// functionRecord is one function declaration written "-> Variant".
type functionRecord struct {
	Project    string `json:"project"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Name       string `json:"name"`
	Origin     string `json:"origin"`
	Inner      bool   `json:"inner"`
	Static     bool   `json:"static"`
	Abstract   bool   `json:"abstract"`
	Arity      int    `json:"arity"`
	Underscore bool   `json:"underscore"`
	Virtual    string `json:"virtual,omitempty"`
	// Override names the ancestor class declaring the same method name, empty
	// when no project ancestor declares it.
	Override string `json:"override,omitempty"`
	// AncestryUnresolved reports that the ancestor walk stopped at a base it
	// could not resolve to an indexed class, so Override is a lower bound for
	// this function.
	AncestryUnresolved bool `json:"ancestry_unresolved"`
}

// bucket holds the counts for one project and one origin.
type bucket struct {
	Files                   int `json:"files"`
	ParseFailures           int `json:"parse_failures"`
	Lines                   int `json:"lines"`
	Functions               int `json:"functions"`
	NoAnnotation            int `json:"no_annotation"`
	Variant                 int `json:"variant"`
	VariantUnderscore       int `json:"variant_underscore"`
	VariantConfirmedVirtual int `json:"variant_confirmed_virtual"`
	VariantOverride         int `json:"variant_override"`
	VariantInner            int `json:"variant_inner"`
	Lambdas                 int `json:"lambdas"`
	LambdaVariant           int `json:"lambda_variant"`
	LambdaNoAnnotation      int `json:"lambda_no_annotation"`
}

type projectResult struct {
	Name        string                    `json:"name"`
	Root        string                    `json:"root"`
	Buckets     map[string]*bucket        `json:"buckets"`
	NameCounts  map[string]map[string]int `json:"variant_name_counts"`
	Functions   []functionRecord          `json:"variant_functions"`
	FailedPaths []string                  `json:"failed_paths"`
}

func newProjectResult(name, root string) *projectResult {
	result := &projectResult{
		Name:       name,
		Root:       root,
		Buckets:    map[string]*bucket{},
		NameCounts: map[string]map[string]int{},
	}
	for _, from := range origins {
		result.Buckets[from.String()] = &bucket{}
		result.NameCounts[from.String()] = map[string]int{}
	}
	return result
}

func (result *projectResult) side(from origin) *bucket {
	return result.Buckets[from.String()]
}

func (result *projectResult) names(from origin) map[string]int {
	return result.NameCounts[from.String()]
}

func main() {
	output := flag.String("out", "", "write the full JSON result to this file")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: variantcensus [-out file] name=root [name=root ...]")
		os.Exit(2)
	}
	results := []*projectResult{}
	for _, argument := range flag.Args() {
		name, root, found := strings.Cut(argument, "=")
		if !found {
			fmt.Fprintf(os.Stderr, "malformed project argument %q\n", argument)
			os.Exit(2)
		}
		result, err := census(name, root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			os.Exit(1)
		}
		results = append(results, result)
	}
	for _, result := range results {
		report(result)
	}
	reportTotals(results)
	if *output == "" {
		return
	}
	encoded, err := json.MarshalIndent(results, "", "  ")
	if err == nil {
		err = os.WriteFile(*output, encoded, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func census(name, root string) (*projectResult, error) {
	paths, err := scriptPaths(root)
	if err != nil {
		return nil, err
	}
	tracked, err := trackedScripts(root)
	if err != nil {
		return nil, err
	}

	result := newProjectResult(name, root)
	classes := map[string]*classRecord{}
	byClassName := map[string]*classRecord{}
	files := map[string]*ast.File{}
	sources := map[string][]byte{}

	for _, path := range paths {
		relative := relativeTo(root, path)
		from := classify(relative, tracked[relative])
		side := result.side(from)
		side.Files++
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		side.Lines += strings.Count(string(source), "\n")
		file, parseErr := gdparser.ParseFile(path, source)
		if parseErr != nil || file == nil {
			side.ParseFailures++
			result.FailedPaths = append(result.FailedPaths, relative)
			continue
		}
		files[relative] = file
		sources[relative] = source
		record := indexTopLevelClass(relative, from, file)
		classes[relative] = record
		if record.hasClassName {
			if _, duplicate := byClassName[record.className]; !duplicate {
				byClassName[record.className] = record
			}
		}
	}

	for relative, file := range files {
		record := classes[relative]
		source := sources[relative]
		from := record.origin
		side := result.side(from)
		topLevel := map[*ast.FunctionDeclaration]bool{}
		for _, statement := range file.Statements {
			if declaration, ok := statement.(*ast.FunctionDeclaration); ok {
				topLevel[declaration] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.FunctionDeclaration:
				side.Functions++
				switch declaration.ReturnType {
				case "":
					side.NoAnnotation++
				case "Variant":
					inner := !topLevel[declaration]
					entry := functionRecord{
						Project:    name,
						Path:       relative,
						Line:       lineAt(source, declaration.Span().Start.Offset),
						Name:       declaration.Name,
						Origin:     from.String(),
						Inner:      inner,
						Static:     declaration.Static,
						Abstract:   declaration.Abstract,
						Arity:      len(declaration.Parameters),
						Underscore: strings.HasPrefix(declaration.Name, "_"),
					}
					entry.Virtual = confirmedVariantVirtuals[declaration.Name]
					if !inner {
						entry.Override, entry.AncestryUnresolved =
							findOverride(record, declaration.Name, classes, byClassName)
					}
					side.Variant++
					if inner {
						side.VariantInner++
					}
					if entry.Underscore {
						side.VariantUnderscore++
					}
					if entry.Virtual != "" {
						side.VariantConfirmedVirtual++
					}
					if entry.Override != "" {
						side.VariantOverride++
					}
					result.names(from)[declaration.Name]++
					result.Functions = append(result.Functions, entry)
				}
			case *ast.LambdaExpression:
				side.Lambdas++
				switch declaration.ReturnType {
				case "":
					side.LambdaNoAnnotation++
				case "Variant":
					side.LambdaVariant++
				}
			}
			return true
		})
	}
	sort.Slice(result.Functions, func(first, second int) bool {
		left, right := result.Functions[first], result.Functions[second]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		return left.Line < right.Line
	})
	sort.Strings(result.FailedPaths)
	return result, nil
}

func scriptPaths(root string) ([]string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".gd") {
			paths = append(paths, path)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

// trackedScripts is the set of .gd paths git tracks under root, keyed relative
// to root. Tracking is what separates authored code from generated code: in
// this corpus every installed addon and all of the generated protocol code sit
// in gitignored directories.
func trackedScripts(root string) (map[string]bool, error) {
	listing, err := exec.Command("git", "-C", root, "ls-files", "--full-name", "-z", "*.gd").Output()
	if err != nil {
		return nil, err
	}
	prefix, err := exec.Command("git", "-C", root, "rev-parse", "--show-prefix").Output()
	if err != nil {
		return nil, err
	}
	within := strings.TrimSpace(string(prefix))
	tracked := map[string]bool{}
	for _, entry := range strings.Split(string(listing), "\x00") {
		if entry == "" {
			continue
		}
		if relative, found := strings.CutPrefix(entry, within); found {
			tracked[relative] = true
		}
	}
	return tracked, nil
}

// classify decides a file's origin. An addons segment names an installed addon
// whatever its tracking status, which keeps an addon repository's own tracked
// product under addons/<name> out of the authored bucket of a project that
// merely installs it.
func classify(relative string, tracked bool) origin {
	for _, segment := range strings.Split(relative, "/") {
		if segment == "addons" {
			return vendored
		}
	}
	if tracked {
		return authored
	}
	return generated
}

func relativeTo(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

// indexTopLevelClass records a file's outermost class: its class_name, its
// base, its preload aliases, and every method name it declares directly.
func indexTopLevelClass(relative string, from origin, file *ast.File) *classRecord {
	record := &classRecord{
		path:           relative,
		origin:         from,
		preloadAliases: map[string]string{},
		methods:        map[string]bool{},
	}
	var extendsExpression ast.Expression
	for _, statement := range file.Statements {
		switch node := statement.(type) {
		case *ast.Directive:
			// gdparser models class_name and extends as one Directive keyed
			// by the keyword, and a single line may carry both.
			switch node.Name {
			case "class_name":
				if identifier, ok := node.Value.(*ast.Identifier); ok {
					record.className, record.hasClassName = identifier.Name, true
				}
				if node.Extends != nil {
					extendsExpression = node.Extends
				}
			case "extends":
				extendsExpression = node.Value
			}
		case *ast.FunctionDeclaration:
			record.methods[node.Name] = true
		case *ast.VariableDeclaration:
			if alias, target, ok := preloadAlias(relative, node); ok {
				record.preloadAliases[alias] = target
			}
		}
	}
	name, path, chain := parseExtends(extendsExpression)
	record.extendsName, record.extendsChain = name, chain
	if path != "" {
		record.extendsPath = resolveScriptPath(relative, path)
	}
	return record
}

// preloadAlias reads "const Name = preload(path)", which Godot treats as a
// type and which may therefore appear as a base class.
func preloadAlias(relative string, declaration *ast.VariableDeclaration) (string, string, bool) {
	if !declaration.Constant {
		return "", "", false
	}
	call, ok := declaration.Value.(*ast.CallExpression)
	if !ok || len(call.Arguments) != 1 {
		return "", "", false
	}
	callee, ok := call.Callee.(*ast.Identifier)
	if !ok || callee.Name != "preload" {
		return "", "", false
	}
	target, ok := literalText(call.Arguments[0])
	if !ok || !strings.HasSuffix(target, ".gd") {
		return "", "", false
	}
	return declaration.Name, resolveScriptPath(relative, target), true
}

// parseExtends reads the base-class form out of the tree rather than the
// source text: a base is a string literal or an identifier, optionally wrapped
// in a member chain naming an inner class.
func parseExtends(expression ast.Expression) (name string, path string, chain []string) {
	for expression != nil {
		switch node := expression.(type) {
		case *ast.MemberExpression:
			chain = append([]string{node.Property}, chain...)
			expression = node.Object
		case *ast.Identifier:
			return node.Name, "", chain
		case *ast.Literal:
			text, ok := literalText(node)
			if !ok {
				return "", "", nil
			}
			return "", text, chain
		default:
			return "", "", nil
		}
	}
	return "", "", nil
}

func literalText(expression ast.Expression) (string, bool) {
	literal, ok := expression.(*ast.Literal)
	if !ok || literal.Kind != ast.StringLiteral {
		return "", false
	}
	raw := literal.Raw
	if literal.RawPrefix {
		raw = strings.TrimPrefix(raw, "r")
	}
	quote := string(literal.Quote)
	if literal.Triple {
		quote = strings.Repeat(quote, 3)
	}
	return strings.TrimSuffix(strings.TrimPrefix(raw, quote), quote), true
}

func resolveScriptPath(from, target string) string {
	if after, found := strings.CutPrefix(target, "res://"); found {
		return after
	}
	return filepath.ToSlash(filepath.Join(filepath.Dir(from), target))
}

// findOverride walks a class's project ancestors looking for another
// declaration of the method name. It reports the first ancestor declaring it,
// and whether the walk stopped at a base it could not resolve to an indexed
// class — in which case the answer is a lower bound.
func findOverride(
	record *classRecord,
	method string,
	classes map[string]*classRecord,
	byClassName map[string]*classRecord,
) (string, bool) {
	seen := map[string]bool{record.path: true}
	current := record
	for {
		if len(current.extendsChain) > 0 {
			// The base names an inner class, which is not indexed.
			return "", true
		}
		var next *classRecord
		switch {
		case current.extendsPath != "":
			next = classes[current.extendsPath]
			if next == nil {
				return "", true
			}
		case current.extendsName != "":
			if alias, ok := current.preloadAliases[current.extendsName]; ok {
				next = classes[alias]
				if next == nil {
					return "", true
				}
				break
			}
			resolved, ok := byClassName[current.extendsName]
			if !ok {
				// An engine type, or a global the index does not hold.
				return "", false
			}
			next = resolved
		default:
			// No base at all, so the implicit base is RefCounted.
			return "", false
		}
		if seen[next.path] {
			return "", true
		}
		seen[next.path] = true
		if next.methods[method] {
			label := next.className
			if label == "" {
				label = next.path
			}
			return label, false
		}
		current = next
	}
}

func lineAt(source []byte, offset int) int {
	if offset > len(source) {
		offset = len(source)
	}
	return strings.Count(string(source[:offset]), "\n") + 1
}

func report(result *projectResult) {
	fmt.Printf("== %s (%s)\n", result.Name, result.Root)
	for _, from := range origins {
		printBucket(from.String(), result.side(from))
	}
	if len(result.FailedPaths) > 0 {
		fmt.Printf("  parse failures: %v\n", result.FailedPaths)
	}
	for _, from := range origins {
		printNames(from.String(), result.NameCounts[from.String()])
	}
	fmt.Println()
}

func reportTotals(results []*projectResult) {
	fmt.Println("== corpus totals")
	for _, from := range origins {
		total := &bucket{}
		for _, result := range results {
			side := result.side(from)
			total.Files += side.Files
			total.ParseFailures += side.ParseFailures
			total.Lines += side.Lines
			total.Functions += side.Functions
			total.NoAnnotation += side.NoAnnotation
			total.Variant += side.Variant
			total.VariantUnderscore += side.VariantUnderscore
			total.VariantConfirmedVirtual += side.VariantConfirmedVirtual
			total.VariantOverride += side.VariantOverride
			total.VariantInner += side.VariantInner
			total.Lambdas += side.Lambdas
			total.LambdaVariant += side.LambdaVariant
			total.LambdaNoAnnotation += side.LambdaNoAnnotation
		}
		printBucket(from.String(), total)
	}
}

func printBucket(label string, side *bucket) {
	fmt.Printf("  %-10s files=%d parse_failures=%d lines=%d functions=%d"+
		" no_annotation=%d variant=%d underscore=%d confirmed_virtual=%d"+
		" override=%d inner=%d lambdas=%d lambda_variant=%d lambda_no_annotation=%d\n",
		label, side.Files, side.ParseFailures, side.Lines, side.Functions,
		side.NoAnnotation, side.Variant, side.VariantUnderscore,
		side.VariantConfirmedVirtual, side.VariantOverride, side.VariantInner,
		side.Lambdas, side.LambdaVariant, side.LambdaNoAnnotation)
}

func printNames(label string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(first, second int) bool {
		if counts[names[first]] != counts[names[second]] {
			return counts[names[first]] > counts[names[second]]
		}
		return names[first] < names[second]
	})
	fmt.Printf("  %s: %d distinct -> Variant names\n", label, len(names))
	for _, name := range names {
		fmt.Printf("    %4d  %s\n", counts[name], name)
	}
}
```

with a `go.mod` requiring `github.com/cafecito-games/gdparser v0.1.6`, the
version this repository is on.

### One cross-check worth recording

Grepping uzir's non-addon files for `func ` lines carrying no `->` returns
**860**. The AST walk returns **16**. The grep is wrong: 844 of its hits are
the first line of a signature the formatter wrapped across several lines, where
the `->` sits on the closing line. Filtering those out — dropping any hit
ending in `(` or `,` — leaves exactly 16, and they are the same 16. That is the
concrete reason this census had to be an AST walk rather than a grep.

`-> Variant` is the one figure a grep does get right, because it is short
enough never to be wrapped:

```sh
find . -name '*.gd' -not -path './addons/*' -not -path './protocol/*' -print0 \
  | xargs -0 grep -hE "^[[:space:]]*(static )?func .*-> *Variant:" | wc -l
```

returns 22 in uzir, matching the walk's authored count exactly.

## 3. Results

Every number below is the program's output. No figure is estimated.

### Per origin, whole corpus

| | authored | generated | vendored |
| --- | --- | --- | --- |
| files | 1766 | 688 | 172 |
| parse failures | 0 | 0 | 0 |
| lines | 234,650 | 224,485 | 16,867 |
| function declarations | 16,718 | 8,229 | 1,392 |
| no `->` annotation | 16 | 0 | 106 |
| `-> Variant` | **24** | **0** | **42** |
| of those, leading underscore | 22 | 0 | 15 |
| of those, confirmed engine virtual | 2 | 0 | 0 |
| of those, in an inner class | 2 | 0 | 4 |
| of those, **plausible project override** | **0** | **0** | **10** |

Totals: 2626 files, 476,002 lines, 26,339 function declarations, 0 parse
failures.

### The same table with the two known duplications removed

Adding aseprite-importer's own 12 `spritesheetology` files back to authored,
and dropping its second copy of `vest` and its second copy of
`spritesheetology`:

| | authored | generated | vendored |
| --- | --- | --- | --- |
| files | 1778 | 688 | 96 |
| function declarations | 16,782 | 8,229 | 909 |
| no `->` annotation | 16 | 0 | 2 |
| `-> Variant` | **24** | **0** | **31** |
| of those, confirmed engine virtual | 2 | 0 | 0 |
| of those, **plausible project override** | **0** | **0** | **5** |

### Per project, authored code only

| | uzir | aseprite-importer | BaristaScript |
| --- | --- | --- | --- |
| files | 1755 | 3 (+12 see above) | 8 |
| lines | 226,304 | 272 | 8,074 |
| function declarations | 16,521 | 13 | 184 |
| no `->` annotation | 16 | 0 | 0 |
| `-> Variant` | 22 | 0 | 2 |
| leading underscore | 20 | 0 | 2 |
| confirmed engine virtual | 2 | 0 | 0 |
| plausible project override | 0 | 0 | 0 |
| distinct `-> Variant` names | 18 | 0 | 2 |

### How rare it is

In authored code, `-> Variant` is **0.14%** of function declarations (24 of
16,718) and no-annotation-at-all is **0.096%** (16 of 16,718). In vendored code
`-> Variant` is **3.0%** (42 of 1,392) and no annotation is **7.6%** (106 of
1,392).

Two things follow. First, `-> Variant` is rare in absolute terms everywhere.
Second, in authored code it is *more common than the hole
`require-return-type` already reports* — 24 against 16 — so the baseline
population the new rule would add to is of the same order as the one that
exists. In vendored code the relationship inverts: no annotation outnumbers
`-> Variant` 106 to 42.

### The override finding

**Zero.** No `-> Variant` in any authored code in this corpus has a project
ancestor that declares the same method name.

The shape is not imaginary — it just lives in the vendored bucket, in exactly
one place. `vest` declares `get_value() -> Variant` on its `VestMeasure` base
and five measure subclasses override it:

| Subclass | Line |
| --- | --- |
| `addons/vest/measures/average-measure.gd` | 14 |
| `addons/vest/measures/max-measure.gd` | 14 |
| `addons/vest/measures/min-measure.gd` | 14 |
| `addons/vest/measures/sum-measure.gd` | 14 |
| `addons/vest/measures/value-measure.gd` | 13 |

Those five are the whole of the de-duplicated override count. One addon, one
base method. The ten in the raw table are these five counted twice, once per
installed copy of `vest`.

The index also *misses* one real instance, which matters more than the five it
finds. `script_templates/VestMeasure/empty.gd:11` declares
`get_value() -> Variant` and is a Godot script template for subclassing
`VestMeasure` — shape three, and the clearest single instance in the corpus.
The index reports no override for it because its base is written `extends
_BASE_`, the editor's template placeholder, which resolves to nothing. That is
the crude index behaving as designed: it reported `ancestry_unresolved` rather
than a confident "no".

### The engine-virtual split

Of the 24 authored `-> Variant` declarations, **2** match a name the Godot 4
class reference documents as a `Variant`-returning virtual, and both are
`_get`:

- `features/auth/support/tests/pack_compatibility_test.gd:17`
- `features/auth/tests/auth_coordinator_test.gd:2013`

**22** have a leading underscore. So the convention is carrying 20 of the 22 on
its own, and the convention is wrong about all 20: names like
`_parse_json_payload`, `_visual_bounds` and `_resolve_position` are private
project helpers, not engine virtuals. The leading underscore in this corpus
marks a private helper, and it tells you nothing about whether the engine
declares the method. A rule that exempted
`_`-prefixed names in order to spare engine virtuals would have exempted 22 of
24 authored declarations and spared almost nothing it meant to.

The confirmed list is documentation knowledge, not a measurement: `_get`,
`_property_get_revert`, `_iter_get`, `_get_drag_data`,
`ResourceFormatLoader._load`, and three `ScriptLanguageExtension`-family names.
Matching is by name only — nothing checks that the declaring class actually
inherits the type the virtual belongs to.

### Lambdas, for completeness

Authored code holds 1,067 lambda expressions: 7 declared `-> Variant` and 78
with no annotation at all. They are excluded from every figure above.
`require-return-type` covers lambdas, so a rule on `-> Variant` would have to
decide about these 7 separately.

## 4. The hand-classified sample

22 distinct functions, read in full. Shape **U** is a genuine union, **P** a
placeholder, **O** forced by an override.

### Authored

**U — a value type that has to be nullable.** Godot 4 has no `Vector3i?`, and
`Vector3i` cannot hold null, so "a tile or nothing" has exactly one spelling.

`features/game/minimap/minimap_walkability_store.gd:221`
```gdscript
func nearest_walkable(origin: Vector3i, max_radius: int) -> Variant:
	if is_walkable(origin):
		return origin
	...
	return null
```

`features/game/ui/in_game/projectile_vfx_player.gd:101` — the same shape with
`Vector2`:
```gdscript
func _resolve_position(entity_id: int, tile: TilePosition) -> Variant:
	if entity_id != 0 and _sprite_source != null:
		var sprite := _sprite_source.sprite(entity_id)
		if sprite != null:
			return sprite.global_position + sprite.sprite_center_offset()
	if tile != null:
		return _tile_to_world(tile.x, tile.y)
	return null
```

**U — a result-or-failure union, GDScript having no sum type.** This is the
largest group: 10 of the 24.

`features/asset_packs/asset_pack_client.gd:108`
```gdscript
func _parse_asset_packs_outcome(code: int, body: PackedByteArray) -> Variant:
	var parsed: Variant = _parse_json_payload(code, body)
	if parsed is AssetPackFailure:
		return parsed
	...
	return AssetPacksFetched.new(dto.realms)
```

`features/auth/auth_client.gd:472`
```gdscript
func _parse_complete_profile_outcome(response: AuthenticationKitAuthorizedResponse) -> Variant:
	var failure := _failure_from_response_or_null(response)
	if failure != null:
		return failure
	var parsed: Variant = _parse_json(response.body)
	if parsed is AuthRequestFailure:
		return parsed
```

Also `features/auth/auth_client.gd:355`, `:387`, `:403`, `:421`, `:453` and
`features/asset_packs/asset_pack_client.gd:142`, all the same shape.

**U — `JSON.data` is declared `Variant`.** `features/auth/auth_client.gd:509`
```gdscript
func _parse_json(body: PackedByteArray) -> Variant:
	var text := body.get_string_from_utf8()
	var json := JSON.new()
	if json.parse(text) != OK:
		return _invalid_response("non-JSON body: %s" % text)
	var parsed: Variant = json.data
	if not parsed is Dictionary and not parsed is Array:
		return _invalid_response("non-JSON body: %s" % text)
	return parsed
```

`features/wallet/wallet_client.gd:143` is the narrower version — `Dictionary`,
`Array` or `null`, so still not narrowable:
```gdscript
func _parse_json(body: PackedByteArray) -> Variant:
	var json := JSON.new()
	if json.parse(body.get_string_from_utf8()) != OK:
		return null
	return json.data
```

`features/asset_packs/asset_pack_client.gd:185` returns `json.data` or an
`AssetPackFailure` — both at once.

**U — forwarding an engine API that returns `Variant`.**
`features/game/ui/fullness/tests/fullness_indicator_test.gd:40`
```gdscript
func _shader_saturation(indicator: FullnessIndicator) -> Variant:
	var material := indicator.icon.material as ShaderMaterial
	expect_not_null(material)
	return material.get_shader_parameter(&"saturation")
```
`:46` is `_shader_empty_brightness`, identical in shape.
`ShaderMaterial.get_shader_parameter` is documented in the Godot 4 class
reference as returning `Variant`, so narrowing would need a cast.

**U — forwarding a dynamic `Object.call`.**
`features/game/entities/tests/character_sprite_test.gd:1301`
```gdscript
func _visual_bounds(sprite: CharacterSprite) -> Variant:
	expect_true(sprite.has_method("visual_bounds_local"))
	if not sprite.has_method("visual_bounds_local"):
		return null
	return sprite.call("visual_bounds_local")
```
`:1308` is `_nameplate_top`, and
`features/game/avatar/tests/player_avatar_test.gd:143` and
`features/game/entities/tests/character_sprite_player_avatar_test.gd:126`
repeat `_visual_bounds` verbatim. `Object.call` is documented as returning
`Variant`, and the `null` branch is reached when the method is absent, so there
is no narrower type.

**O — a confirmed engine virtual.**
`features/auth/tests/auth_coordinator_test.gd:2013`
```gdscript
class MalformedManifestStub extends RefCounted:
	func _get(property: StringName) -> Variant:
		if property == &"schema_version":
			return PackManifest.CURRENT_SCHEMA_VERSION
		if property == &"pack_format_version":
			return "seven"
		return null
```
`features/auth/support/tests/pack_compatibility_test.gd:17` is the same
`_get(property: StringName) -> Variant` over a `Dictionary`. `Object._get` is
documented as returning `Variant`; a narrower return would not satisfy it.
Both are inner classes, which is relevant: the base here is `RefCounted`, an
engine type, so no project-level inheritance index would resolve this either —
only a table of engine virtuals would.

**O — forced by a project base, and the one the index missed.**
`script_templates/VestMeasure/empty.gd:11`
```gdscript
extends _BASE_
class_name _CLASS_

func get_measure_name() -> String:
  return "custom"

func get_value() -> Variant:
  return 0.0
```
The body returns a `float`, so `-> float` would be writable *if the base
allowed it*, and `VestMeasure.get_value` declares `Variant`. The file is a
Godot script template; `script_templates/**` is in the `exclude` list of uzir's
`.gdkit/lint.json`, so no gdkit rule reads it today. Its `extends _BASE_`
placeholder is why the index could not resolve the base.

**U, with a caveat — crossing a `Callable` boundary.**
`BaristaScript/project/tests/corpus_harness.gd:321`
```gdscript
func _evaluate(case_path: String, stage: String) -> Variant:
	if case_evaluator.is_valid():
		return case_evaluator.call(case_path)
	return _evaluate_with_language(case_path, stage)
```
`:346` is `_evaluate_with_language`, which returns `Dictionary` literals on its
error paths and otherwise forwards `probe.evaluate_corpus(...)` through a
duck-typed call. This pair is the closest the corpus comes to a placeholder:
the consumer at `:297` immediately validates the shape —
```gdscript
	var evaluation: Variant = _evaluate(case_info["path"], case_info["stage"])
	var shape_error := _result_error(evaluation, case_info["stage"])
```
— and `_result_error` opens with `if not result is Dictionary`. So `Dictionary`
is the intended type. But `Callable.call` is documented as returning `Variant` and the forwarded
`probe.evaluate_corpus` is reached through duck typing, so narrowing the
signature would require a cast the author deliberately did not write: the
validator exists *because* the value is untrusted. Classified **U**, with the
note that this is the one case where a reader could argue for **P**.

### Vendored

**U — the base of a measure hierarchy, where `Variant` is honest.**
`addons/vest/measures/measure.gd:31`
```gdscript
## Get the value of the measure.
## [i]override[/i]
func get_value() -> Variant:
	return null
```

**O — and its five subclasses, forced.**
`addons/vest/measures/average-measure.gd:14`
```gdscript
extends VestMeasure

func get_value() -> Variant:
  return _sum / _count
```
`_sum / _count` is numeric, so `-> float` is what the body wants, and the base
forbids it. `max-measure.gd:14`, `min-measure.gd:14`, `sum-measure.gd:14` and
`value-measure.gd:13` are the same.

**O, transitively.** `addons/vest/vest-defs.gd:287`
```gdscript
	func get_measurement(metric: StringName, measurement: StringName) -> Variant:
		for measure in _measures:
			if measure.get_metric_name() == metric and measure.get_measure_name() == measurement:
				return measure.get_value()
		assert(false, "Measurement not found!")
		return null
```
It returns `measure.get_value()`, so it inherits the base's `Variant` through
the value rather than through a signature. No override analysis would flag this
one: it overrides nothing.

**U — a bool-or-error union.** `addons/vest/vest-matchers.gd:10`
```gdscript
static func is_empty(object: Variant) -> Variant:
	if _is_builtin_container(object) or _is_stringlike(object):
		return object.is_empty()
	elif object is Object:
		if object.has_method("is_empty"):
			return object.is_empty()
		else:
			return ERR_METHOD_NOT_FOUND
	else:
		return ERR_CANT_RESOLVE
```
`contains` at `:21` is the same. A `bool` result or a Godot `Error` constant,
which is an `int`.

**U — a recursive serializer over arbitrary input.**
`addons/vest/vest-data-serializer.gd:12`
```gdscript
static func serialize(data: Variant, max_depth: int = MAX_DEPTH, emit_error: bool = true) -> Variant:
```
It dispatches on `typeof(data)` and returns a builtin of whatever shape the
input had.

**U — a mock returning whatever it was configured with.**
`addons/vest/mocks/vest-mock-defs.gd:48`
```gdscript
	func _get_answer(args: Array) -> Variant:
		if _answer_method:
			return _answer_method.call(args)
		else:
			return _answer_value
```

**U — `Dictionary` or `null`.**
`addons/AuthenticationKit/AuthenticationKitAuthorizedResponse.gd:33`
```gdscript
## Parses [member body] as a JSON object and returns the resulting Dictionary,
## or [code]null[/code] when the body is empty or not a valid JSON object.
func json() -> Variant:
```
The docstring states the union explicitly.

**U — result-or-failure again, 18 times in one addon.**
`addons/AuthenticationKit/internal/AuthenticationKitBackendAuthClient.gd:56`
```gdscript
func request_nonce() -> Variant:
	var raw: Variant = await _perform_json_request(
		HTTPClient.METHOD_POST, nonce_path, null, PackedStringArray()
	)
	if raw is AuthenticationKitAuthFailure:
		return raw
	var payload: Dictionary = raw
	return _parse_nonce_response(payload)
```
`login_email_password`, `signup_email_password`, `exchange_sso`, `refresh`,
`logout` and the eleven internal helpers in that file and in
`AuthenticationKitBackendDesktop.gd` are all this shape.

### Tally of the sample

| Shape | Authored | Vendored |
| --- | --- | --- |
| U, genuine union | 21 of 24 | all but five |
| O, forced by an override | 3 (2 engine `_get`, 1 script template) | 5 (`vest` measures) |
| P, placeholder | **0** | **0** |

Not one unambiguous placeholder in 66 `-> Variant` declarations across 476,002
lines.

## 5. Recommendation

**Nothing — do not add the rule.** Not in `lint`, not in `generate`.

The reasoning, in the order the evidence forces it.

**The premise of #37 does not hold in this corpus.** The issue says `-> Variant`
"is usually a conversion artifact". Here it is usually the opposite: 21 of 24
authored declarations are a union GDScript has no other way to spell, and the
remaining 3 are forced by a base. A rule reporting `-> Variant` would have
produced 24 findings across 16,718 authored functions and been wrong about
every one of them. That is not a strict rule; it is a rule with a 100%
false-positive rate on its only measured population.

**The shapes that make it unfixable are structural, not stylistic.** Three
recur and none can be narrowed by editing the signature:

- A nullable value type. `Vector3i` and `Vector2` are builtin value types, and
  GDScript has no nullable type annotation — documentation knowledge, not
  something this census measured — so `-> Variant` is the only spelling of
  "a position or nothing". `nearest_walkable` and `_resolve_position` are this.
- A result-or-failure union. GDScript has no sum type, so returning either a
  success value or a failure object means `Variant`. This is 10 of 24 authored
  and most of `AuthenticationKit`.
- Forwarding an engine API that itself returns `Variant` — `JSON.data`,
  `Object.call`, `ShaderMaterial.get_shader_parameter`, `Callable.call`. Here
  `-> Variant` is not a weaker type than the truth; it *is* the truth, and a
  narrower annotation would be a lie the engine would not catch either.

A rule cannot tell these from a placeholder by looking at the signature, and
looking at the body means type inference over the return expressions, which is
a different and much larger project than #37 describes.

**The override finding specifically.** Zero in authored code, five in one
vendored addon. Read narrowly, that says a single-file lint rule would be
*sound* here — there is no base class to resolve, because nobody in this
corpus's own code inherits a `Variant`-returning method. But the two
authored override cases that do exist both override `Object._get`, an **engine**
virtual, and an inheritance index over project classes would not have caught
either. So the override shape, in this corpus, needs an engine-virtual table
rather than a project inheritance graph — which is the opposite of what #37
predicted would decide the design, and it does not make the feature
`generate`-shaped. It makes the override question moot, because the rule should
not exist for unrelated reasons.

**Why not a lower severity or a `generate` feature either.** Both still require
a true positive to act on, and the census found none. A `generate`-shaped
feature would additionally carry the cost of a whole-project inheritance pass
to answer a question that has, measurably, zero instances in authored code.

**What `require-return-type` should do instead:** nothing. It is already in the
right place. 16 authored functions have no annotation at all and the rule from
#35 reports exactly those 16: `_ready()`, `_process(delta: float)`, `_draw()`
(three times), `_init()`, `_validate_property(property)`,
`set_progressing(to_value: float)`, `_update_status()`,
`_update_progress_border()`, `_update_children_size()`,
`_should_use_editor_theme()`, `suite()`, `test_case_a()`, `test_case_b()` and
`test_vest_wiring()`. Each is a plain omission on a function with no return
value, so each has a writable annotation. That is a clean population. Widening
the rule to 24 declarations with no writable alternative would dilute it.

### Evidence that would change this

Stated so the recommendation is falsifiable rather than final.

- **A project that has actually been through the conversion.** uzir has *not*
  enabled the typing rules: its `.gdkit/lint.json` lists only
  `no-engine-logging` under `enable`, and `require-return-type` ships inert.
  So the 24 authored `-> Variant` declarations are a project's natural habit,
  measured before any rule pushed on it. Git blame confirms it: all 22 in uzir
  were introduced by feature commits between 2026-05-01 and 2026-09-23 — the
  oldest is `3d18dfee3 feat(client): launcher PCK fetch for asset pack
  distribution (#227)` — and none by `42937dda8 chore(client): adopt 24 more
  gdkit lint rules`. The one attributed to `7c5a1e8f7 style(client): format
  every client GDScript file with gdkit` is attributed there only because the
  formatter rewrapped the signature line. **This census therefore cannot test
  the conversion-artifact hypothesis at all**: there is no conversion in it.
  A project that enabled `require-return-type` and annotated its way to clean
  is the one corpus that would settle #37, and no such project exists on this
  machine. If one appears and its newly written `-> Variant` declarations
  outnumber its honest unions, the recommendation flips.
- **An authored override count above zero.** If a corpus shows projects
  declaring `Variant`-returning methods on their own base classes and
  subclasses forced to match, the design question #37 raised becomes live and
  the answer is a `generate`-shaped feature, not a lint rule.
- **A corpus from outside this organization.** All three projects share
  authors, a style guide and in two cases a gdkit configuration. The union
  idiom here (`_parse_*_outcome` returning a value or a failure object) is one
  team's convention. A codebase that spells errors differently could have an
  entirely different `-> Variant` population.
- **A narrower rule with a measured target.** If a future census can isolate a
  shape that *is* mechanically narrowable — say, a function whose every
  `return` is a literal of one concrete type — that is a different and
  defensible rule, and the number to measure is how many such functions exist.
  This census did not measure it.

## 6. Limits

- **One codebase family is almost the whole corpus.** uzir supplies 1755 of the
  1766 authored files (99.4%) and 22 of the 24 authored `-> Variant`
  declarations. The other
  two projects contribute 11 files and 2 declarations between them. Every
  authored ratio in section 3 is a fact about uzir with a rounding error beside
  it. BaristaScript's 8 files are a single test harness; aseprite-importer's
  authored code is 13 functions.
- **The override index is crude, and here is exactly how.** It indexes only
  each file's outermost class, so a method on an inner class is never a
  resolvable base — and both authored engine-virtual cases are in inner
  classes. It resolves a base only through a `class_name`, a `res://` or
  relative string literal, and a `const X = preload(...)` alias; an autoload
  identifier, an inner-class base (`Outer.Inner`), and a template placeholder
  (`extends _BASE_`) all fail, the last of which is why it missed
  `script_templates/VestMeasure/empty.gd:11`. It matches on method *name* only,
  so it does not check arity or staticness, meaning it would over-report an
  unrelated same-named method as an override. It keeps the first class for a
  duplicated `class_name` rather than reporting the ambiguity. And it knows
  nothing about engine classes: a base of `RefCounted`, `Node` or `Control`
  ends the walk with "no override", which is the *correct* answer about project
  ancestry and the *wrong* answer about whether a signature is forced.
- **A convention is standing in for engine knowledge, and it does not work.**
  2 of 24 authored declarations match a confirmed `Variant`-returning virtual;
  22 of 24 have a leading underscore. The gap is 20 declarations where the
  underscore means "private project helper". Both counts are reported
  separately in section 3 so the reader can see how little the convention
  carries.
- **The confirmed-virtual list is documentation knowledge, not a measurement.**
  It holds `_get`, `_property_get_revert`, `_iter_get`, `_get_drag_data`,
  `ResourceFormatLoader._load`, `ScriptLanguageExtension._instantiate`, a
  `ScriptLanguageExtension`-family `_call`, and
  `ScriptExtension._get_default_property`. It is not the complete set of
  `Variant`-returning virtuals in Godot 4 — `ScriptLanguageExtension` alone has
  more — and matching is by bare name, so a project method named `_load` would
  be counted as a virtual whether or not its class is a `ResourceFormatLoader`.
  Only `_get` actually matched, so none of that bit here.
- **The vendored totals double-count one addon.** `vest` is installed in two of
  the three projects at two different releases, so the raw vendored row counts
  it twice; section 3 gives both the raw and the de-duplicated figures and the
  de-duplicated one is the honest number.
- **No conversion has happened in this corpus**, which is the measurement #37
  most wanted and the one that could not be taken. Section 5 says so under the
  first falsifier.
- **Lambdas are counted but not classified.** 7 authored lambdas are declared
  `-> Variant` and none of them was read by hand.
- **`:=`-inferred returns are invisible.** GDScript has no inferred return
  type, so this is not a gap in the return census, but the same is not true of
  the variables these functions feed; a `var parsed: Variant = ...` declaration
  is a separate population this census did not count.
- **One snapshot in time.** Every figure is from the working trees as they
  stood on 2026-10-04, at `2e99d75ed` for uzir.
