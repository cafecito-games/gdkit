package failure

import (
	"errors"
	"fmt"
	"testing"
)

func TestWrapCarriesTheKind(t *testing.T) {
	wrapped := Wrap(ConfigParse, errors.New("boom"))
	labelled, ok := Of(wrapped)
	if !ok {
		t.Fatal("Of() found no labelled error")
	}
	if labelled.Kind != ConfigParse {
		t.Errorf("Kind = %q, want %q", labelled.Kind, ConfigParse)
	}
	if labelled.Path != "" || labelled.Key != "" {
		t.Errorf("Wrap set Path or Key: %+v", labelled)
	}
}

func TestWrapPathAndWrapKey(t *testing.T) {
	labelled, ok := Of(WrapPath(ConfigRead, "a/b.json", errors.New("boom")))
	if !ok || labelled.Path != "a/b.json" || labelled.Key != "" {
		t.Fatalf("WrapPath = %+v, %v", labelled, ok)
	}
	labelled, ok = Of(WrapKey(ConfigUnknownKey, "a/b.json", "x[0].y", errors.New("boom")))
	if !ok || labelled.Path != "a/b.json" || labelled.Key != "x[0].y" {
		t.Fatalf("WrapKey = %+v, %v", labelled, ok)
	}
}

// The message is the wrapped error's own, so text output is unchanged by
// labelling and the JSON message cannot drift from it.
func TestErrorMessageIsTheWrappedMessage(t *testing.T) {
	wrapped := Wrap(ConfigInvalid, errors.New("unclassified must be \"error\" or \"ignore\""))
	if got, want := wrapped.Error(), `unclassified must be "error" or "ignore"`; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// A labelled error has to survive further wrapping, because a caller between
// the detecting package and the command may add context.
func TestOfFindsALabelDeeperInTheChain(t *testing.T) {
	inner := errors.New("permission denied")
	wrapped := fmt.Errorf("read config: %w", Wrap(ConfigRead, inner))
	labelled, ok := Of(wrapped)
	if !ok || labelled.Kind != ConfigRead {
		t.Fatalf("Of() = %+v, %v", labelled, ok)
	}
	if !errors.Is(wrapped, inner) {
		t.Error("labelling broke the error chain")
	}
}

func TestWrapOfNilIsNil(t *testing.T) {
	if Wrap(ConfigRead, nil) != nil {
		t.Error("Wrap(nil) is not nil")
	}
	if WrapPath(ConfigRead, "p", nil) != nil {
		t.Error("WrapPath(nil) is not nil")
	}
	if WrapKey(ConfigRead, "p", "k", nil) != nil {
		t.Error("WrapKey(nil) is not nil")
	}
}

func TestOfAnUnlabelledError(t *testing.T) {
	if labelled, ok := Of(errors.New("plain")); ok {
		t.Errorf("Of() = %+v, want no label", labelled)
	}
	if labelled, ok := Of(nil); ok {
		t.Errorf("Of(nil) = %+v, want no label", labelled)
	}
}

// Kind values appear in JSON output, so a rename breaks a consumer. This pins
// the strings themselves rather than only their use.
func TestKindValuesAreStable(t *testing.T) {
	for kind, want := range map[string]string{
		ConfigRead:         "config.read",
		ConfigParse:        "config.parse",
		ConfigUnknownKey:   "config.unknown_key",
		ConfigInvalid:      "config.invalid",
		ConfigVersionFloor: "config.version_floor",
		UsageArguments:     "usage.arguments",
		UsageFormat:        "usage.format",
		UsageVersionFloor:  "usage.version_floor",
		ProjectLoad:        "project.load",
		AnalysisFailed:     "analysis.failed",
		OutputWrite:        "output.write",
		FileWrite:          "file.write",
	} {
		if kind != want {
			t.Errorf("kind = %q, want %q", kind, want)
		}
	}
}
