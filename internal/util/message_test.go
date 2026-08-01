package util

import (
	"image/color"
	"testing"

	"github.com/sentinelb51/revoltgo"
)

func TestParseHexColor(t *testing.T) {
	tests := []struct {
		in   string
		want color.Color
		ok   bool
	}{
		{"#fff", color.NRGBA{R: 255, G: 255, B: 255, A: 255}, true},
		{"#f00", color.NRGBA{R: 255, G: 0, B: 0, A: 255}, true},
		{"#5B7CFA", color.NRGBA{R: 0x5B, G: 0x7C, B: 0xFA, A: 255}, true},
		{"#000000", color.NRGBA{A: 255}, true},
		{"5B7CFA", nil, false},  // missing #
		{"#5B7CF", nil, false},  // wrong length
		{"#GGGGGG", nil, false}, // not hex
		{"linear-gradient(red, blue)", nil, false},
		{"", nil, false},
	}

	for _, tt := range tests {
		got, ok := parseHexColor(tt.in)
		if ok != tt.ok {
			t.Errorf("parseHexColor(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("parseHexColor(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestRoleColorPicksMostSeniorColouredRole(t *testing.T) {
	server := &revoltgo.Server{
		Roles: map[string]*revoltgo.ServerRole{
			"admin":    {Rank: 1, Colour: new("#ff0000")},
			"mod":      {Rank: 5, Colour: new("#00ff00")},
			"everyone": {Rank: 10},                         // no colour: skipped
			"broken":   {Rank: 0, Colour: new("gradient")}, // most senior but unparseable
		},
	}

	got, ok := roleColor(server, []string{"mod", "admin", "everyone"})
	if !ok {
		t.Fatal("expected a colour")
	}
	if got != (color.NRGBA{R: 255, A: 255}) {
		t.Errorf("got %v, want admin red (lowest rank wins)", got)
	}

	// The most-senior role has an unparseable colour: it is still selected as
	// "best", so resolution fails — documents current behaviour.
	if _, ok := roleColor(server, []string{"broken", "admin"}); ok {
		t.Error("unparseable senior colour: expected ok=false")
	}

	if _, ok := roleColor(server, []string{"everyone"}); ok {
		t.Error("no coloured roles: expected ok=false")
	}
	if _, ok := roleColor(server, nil); ok {
		t.Error("no roles: expected ok=false")
	}
	if _, ok := roleColor(server, []string{"missing"}); ok {
		t.Error("unknown role id: expected ok=false")
	}
}
