package semantic_test

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/cafecito-games/gdkit/internal/semantic"
	"github.com/cafecito-games/gdkit/internal/semantic/engineschema"
	"github.com/cafecito-games/gdkit/internal/semanticsource"
	"github.com/cafecito-games/gdkit/project"
	"github.com/cafecito-games/gdparser/ast"
)

// TestCorpusReducer measures the reducer against a caller-supplied real
// project. It is intentionally skipped in hermetic CI and only reads the
// corpus. Its external package avoids an import cycle: engineschema publishes
// semantic.Engine, so a same-package semantic test cannot import it.
func TestCorpusReducer(t *testing.T) {
	root := os.Getenv("GDKIT_CORPUS")
	if root == "" {
		t.Skip("GDKIT_CORPUS is not set")
	}
	snapshot, err := project.Load(project.Config{
		Root:       root,
		Exclude:    []string{".worktrees/**", ".claude/**"},
		Identities: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := engineschema.LoadEmbedded(4, 7)
	if err != nil {
		t.Fatal(err)
	}
	source := semanticsource.NewSnapshot(snapshot)
	first := collectReducerCorpusReceipt(semantic.NewAnalyzer(source, loaded.Engine), source)
	second := collectReducerCorpusReceipt(semantic.NewAnalyzer(source, loaded.Engine), source)
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("two reducer corpus receipts differ:\n%s\n%s", firstJSON, secondJSON)
	}
	t.Logf("%s", first.summary())
}

type reducerCorpusReceipt struct {
	Total    int                   `json:"total"`
	Resolved int                   `json:"resolved"`
	Variant  int                   `json:"variant"`
	Unknown  int                   `json:"unknown"`
	Reasons  []reducerCorpusReason `json:"top_unknown_reasons"`
}

type reducerCorpusReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

func collectReducerCorpusReceipt(analyzer *semantic.Analyzer, source semantic.SourceSet) reducerCorpusReceipt {
	receipt := reducerCorpusReceipt{}
	reasons := map[string]int{}
	for _, filePath := range source.Paths() {
		file := source.File(filePath)
		if file == nil {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			expression, ok := node.(ast.Expression)
			if !ok {
				return true
			}
			receipt.Total++
			resolved := analyzer.TypeOf(expression)
			switch resolved.Kind() {
			case semantic.KindUnknown:
				receipt.Unknown++
				reasons[resolved.Reason()]++
			case semantic.KindVariant:
				receipt.Variant++
			default:
				receipt.Resolved++
			}
			return true
		})
	}
	for reason, count := range reasons {
		receipt.Reasons = append(receipt.Reasons, reducerCorpusReason{Reason: reason, Count: count})
	}
	sort.Slice(receipt.Reasons, func(left, right int) bool {
		if receipt.Reasons[left].Count != receipt.Reasons[right].Count {
			return receipt.Reasons[left].Count > receipt.Reasons[right].Count
		}
		return receipt.Reasons[left].Reason < receipt.Reasons[right].Reason
	})
	if len(receipt.Reasons) > 10 {
		receipt.Reasons = receipt.Reasons[:10]
	}
	return receipt
}

func (r reducerCorpusReceipt) summary() string {
	return fmt.Sprintf(
		"reducer corpus: expressions=%d resolved=%d (%.1f%%) Variant=%d (%.1f%%) Unknown=%d (%.1f%%) top_unknown_reasons=%v",
		r.Total,
		r.Resolved,
		reducerCorpusShare(r.Resolved, r.Total),
		r.Variant,
		reducerCorpusShare(r.Variant, r.Total),
		r.Unknown,
		reducerCorpusShare(r.Unknown, r.Total),
		r.Reasons,
	)
}

func reducerCorpusShare(count, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) * 100 / float64(total)
}
