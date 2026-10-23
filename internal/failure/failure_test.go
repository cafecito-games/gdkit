package failure

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapCarriesTheKind(t *testing.T) {
	wrapped := Wrap(ConfigParse, errors.New("boom"))
	labelled, ok := Of(wrapped)
	require.True(t, ok, "Of() found no labelled error")
	assert.Equal(t, ConfigParse, labelled.Kind)
	assert.Empty(t, labelled.Path)
	assert.Empty(t, labelled.Key)
}

func TestWrapPathAndWrapKey(t *testing.T) {
	labelled, ok := Of(WrapPath(ConfigRead, "a/b.json", errors.New("boom")))
	require.True(t, ok)
	require.Equal(t, "a/b.json", labelled.Path)
	require.Empty(t, labelled.Key)

	labelled, ok = Of(WrapKey(ConfigUnknownKey, "a/b.json", "x[0].y", errors.New("boom")))
	require.True(t, ok)
	require.Equal(t, "a/b.json", labelled.Path)
	require.Equal(t, "x[0].y", labelled.Key)
}

// The message is the wrapped error's own, so text output is unchanged by
// labelling and the JSON message cannot drift from it.
func TestErrorMessageIsTheWrappedMessage(t *testing.T) {
	wrapped := Wrap(ConfigInvalid, errors.New("unclassified must be \"error\" or \"ignore\""))
	assert.Equal(t, `unclassified must be "error" or "ignore"`, wrapped.Error())
}

// A labelled error has to survive further wrapping, because a caller between
// the detecting package and the command may add context.
func TestOfFindsALabelDeeperInTheChain(t *testing.T) {
	inner := errors.New("permission denied")
	wrapped := fmt.Errorf("read config: %w", Wrap(ConfigRead, inner))
	labelled, ok := Of(wrapped)
	require.True(t, ok)
	require.Equal(t, ConfigRead, labelled.Kind)
	assert.ErrorIs(t, wrapped, inner, "labelling broke the error chain")
}

func TestWrapOfNilIsNil(t *testing.T) {
	assert.Nil(t, Wrap(ConfigRead, nil), "Wrap(nil) is not nil")
	assert.Nil(t, WrapPath(ConfigRead, "p", nil), "WrapPath(nil) is not nil")
	assert.Nil(t, WrapKey(ConfigRead, "p", "k", nil), "WrapKey(nil) is not nil")
}

func TestOfAnUnlabelledError(t *testing.T) {
	_, ok := Of(errors.New("plain"))
	assert.False(t, ok, "Of() found a label where none was expected")
	_, ok = Of(nil)
	assert.False(t, ok, "Of(nil) found a label where none was expected")
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
		assert.Equal(t, want, kind)
	}
}
