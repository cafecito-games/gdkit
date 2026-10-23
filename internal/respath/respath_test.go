package respath

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolvePlacesEveryFormAReferenceCanTake(t *testing.T) {
	uids := map[string]string{"uid://known": "art/hero.png"}
	cases := []struct {
		name      string
		from      string
		reference string
		want      Resolution
	}{
		{"absolute resource path", "ui/hud.gd", "res://art/heart.png",
			Resolution{Kind: Project, Path: "art/heart.png"}},
		{"relative to the holding file", "ui/hud.gd", "sprites/heart.png",
			Resolution{Kind: Project, Path: "ui/sprites/heart.png"}},
		{"declared identity", "ui/hud.gd", "uid://known",
			Resolution{Kind: Project, Path: "art/hero.png"}},
		{"undeclared identity keeps its only name", "ui/hud.gd", "uid://missing",
			Resolution{Kind: Unresolved, Path: "uid://missing"}},
		{"the player's save directory is not shipped", "ui/hud.gd", "user://save.cfg",
			Resolution{Kind: External}},
		{"another engine scheme", "ui/hud.gd", "http://example.com/a.png",
			Resolution{Kind: External}},
		{"climbing out of the project", "ui/hud.gd", "../../../outside.png",
			Resolution{Kind: Escaping, Path: "../../outside.png"}},
		{"a leading slash is the project root", "ui/hud.gd", "res:///art/heart.png",
			Resolution{Kind: Project, Path: "art/heart.png"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, Resolve(test.from, test.reference, uids))
		})
	}
}

func TestResolveWithNoTableLeavesEveryIdentityUnresolved(t *testing.T) {
	assert.Equal(t, Resolution{Kind: Unresolved, Path: "uid://a"}, Resolve("a.gd", "uid://a", nil))
}

func TestIsReferenceAcceptsOnlyWhatCarriesAScheme(t *testing.T) {
	assert.True(t, IsReference("res://a.png"))
	assert.True(t, IsReference("uid://abc"))
	// A bare relative path is indistinguishable from any other string, and
	// treating every string as a possible path makes reachability meaningless.
	assert.False(t, IsReference("a.png"))
	assert.False(t, IsReference("user://save.cfg"))
	assert.False(t, IsReference(""))
}
