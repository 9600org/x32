package x32

import (
	"regexp"
	"strconv"
	"strings"
)

const (
	OFF int32 = iota
	RD
	GN
	YE
	BL
	MG
	CY
	WH
	OFFi
	RDi
	GNi
	YEi
	BLi
	MGi
	CYi
	WHi
)

// colours is a map of x32 colour name to id.
var colours = map[string]int32{
	"OFF":  OFF,
	"RD":   RD,
	"GN":   GN,
	"YE":   YE,
	"BL":   BL,
	"MG":   MG,
	"CY":   CY,
	"WH":   WH,
	"OFFi": OFFi,
	"RDi":  RDi,
	"GNi":  GNi,
	"YEi":  YEi,
	"BLi":  BLi,
	"MGi":  MGi,
	"CYi":  CYi,
	"WHi":  WHi,
}

// guessIconAndColour uses the configured name hints to guess at appropriate
// icons and colours for the X32 scribble strip based on the passed in name.
func guessColourAndIcon(nameHints []nameHint, name string) (int32, int32) {
	colID := int32(-1)
	iconID := int32(-1)
	if len(strings.TrimSpace(name)) == 0 {
		colID = 0
		iconID = 0
	} else {
		for _, hint := range nameHints {
			if hint.matcher.Match([]byte(strings.ToLower(name))) {
				if colID == -1 && hint.colour > -1 {
					colID = hint.colour
				}
				if iconID == -1 && hint.icon > -1 {
					iconID = hint.icon
				}
				if colID > -1 && iconID > -1 {
					break
				}
			}
		}
	}
	return colID, iconID
}



// buildNameHints parses and constructs an array of nameHints from the passed
// in config.
func buildNameHints(src map[string]string) ([]nameHint, error) {
	r := make([]nameHint, 0, len(src))
	for k, v := range src {
		nh, err := newNameHint(k, v)
		if err != nil {
			return nil, err
		}
		r = append(r, *nh)
	}
	return r, nil
}

// nameHint represents a hint as to which icon and colour should be assingned
// to a scribble strip.
type nameHint struct {
	// matcher is a regex to test track names, a match means that the colour and
	// icon contained in this name hint are eligible for use.
	matcher *regexp.Regexp

	// icon is on
	icon   int32
	colour int32
}

func newNameHint(m, hint string) (*nameHint, error) {
	ret := &nameHint{nil, -1, -1}
	var err error
	ret.matcher, err = regexp.Compile(strings.ToLower(m))
	if err != nil {
		return nil, err
	}
	hintBits := strings.Split(hint, " ")
	if len(hintBits) >= 1 {
		icon, err := strconv.Atoi(hintBits[0])
		if err != nil {
			return nil, err
		}
		ret.icon = int32(icon)
	}
	if len(hintBits) == 2 {
		ret.colour = colours[hintBits[1]]
	}
	return ret, nil
}
