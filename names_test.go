package x32

import (
	"testing"
)

func TestNameHints(t *testing.T) {
	nh, err := buildNameHints(
		map[string]string{
			"bus":   "5", // only icon
			"bass":  "1 RD",
			"drums": "3 BL",
		})
	if err != nil {
		t.Fatalf("failed to create namehints: %v", err)
	}

	for _, test := range []struct {
		name       string
		wantIcon   int32
		wantColour int32
	}{
		{name: "bass guitar", wantIcon: 1, wantColour: RD},
		{name: "banana bass guitar", wantIcon: 1, wantColour: RD},
		{name: "BASS", wantIcon: 1, wantColour: RD},
		{name: "BasS", wantIcon: 1, wantColour: RD},
		{name: "Drum", wantIcon: -1, wantColour: -1},
		{name: "Drumses", wantIcon: 3, wantColour: BL},
		{name: "Big red bus", wantIcon: 5, wantColour: -1}, // "bus" hint  sets icon only
		{name: "Bass bus", wantIcon: 5, wantColour: RD},    // icon from "bus" hint, colour from "bass"
	} {
		t.Run(test.name, func(t *testing.T) {
			gotColour, gotIcon := guessColourAndIcon(nh, test.name)
			if gotIcon != test.wantIcon {
				t.Errorf("got icon %d, want %d", gotIcon, test.wantIcon)
			}
			if gotColour != test.wantColour {
				t.Errorf("got colour %d, want %d", gotColour, test.wantColour)
			}
		})
	}
}
