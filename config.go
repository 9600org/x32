package x32

import (
	"fmt"
	"strconv"
	"strings"
)

// Valid x32 strip types and their X32 -stat starting offset.
var validX32MappingTypes = map[string]int32{
	"ch":      0,
	"bus":     48,
	"dca":     72,
	"mtx":     64,
	"main/m":  70,
	"main/st": 71,
	"auxin":   32,
	"usb":     38,
	"fxrtn":   40,
}

// buildTrackMap returns a trackMap object representing the passed in config.
func buildTrackMap(conf map[string]string) (*trackMap, error) {
	ret := trackMap{
		reaper:       make(map[string]*mapping),
		x32:          make(map[string]*mapping),
		x32StatIndex: make(map[int32]*mapping),
	}
	for k, v := range conf {
		var x32Track, reaTrack []string
		var x32Lo, x32Hi, reaLo, reaHi int32
		var err error

		switch v {
		case "main/st", "main/m":
			x32Track = []string{v}
		default:
			x32Track = strings.Split(v, "/")
			x32Lo, x32Hi, err = parseRange(x32Track[1])
			if err != nil && x32Track[0] != "main" {
				return nil, err
			}
		}
		x32StatIndexBase, ok := validX32MappingTypes[x32Track[0]]
		if !ok {
			return nil, fmt.Errorf("invalid x32 mapping type %s", x32Track[0])
		}

		switch k {
		case "master":
			if x32Lo != x32Hi {
				return nil, fmt.Errorf("mapping for /master should have 1 target, got %d", (x32Hi - x32Lo + 1))
			}
			x32K := x32Track[0]
			if x32Lo > 0 {
				x32K = fmt.Sprintf("%s/%02d", x32Track[0], x32Lo)
			}
			x32StatIndex := x32StatIndexBase
			mapping := &mapping{x32Prefix: x32K, x32StatIndex: x32StatIndex, reaperPrefix: k, reaperTrackIndex: -1}
			ret.reaper[k] = mapping
			ret.x32[x32K] = mapping
			ret.x32StatIndex[x32StatIndex] = mapping
			continue
		default:
			reaTrack = strings.Split(k, "/")
			reaLo, reaHi, err = parseRange(reaTrack[1])
			if err != nil {
				return nil, err
			}
		}
		if err := isReaperType(reaTrack[0]); err != nil {
			return nil, err
		}

		// Handle all other mappings:
		if reaSpan, x32Span := reaHi-reaLo+1, x32Hi-x32Lo+1; reaSpan != x32Span {
			return nil, fmt.Errorf("invalid mapping - reaper range %d != x32 range %d", reaSpan, x32Span)
		}

		for reaN, x32N := reaLo, x32Lo; reaN <= reaHi; reaN, x32N = reaN+1, x32N+1 {
			reaK := fmt.Sprintf("%s/%d", reaTrack[0], reaN)
			x32K := fmt.Sprintf("%s/%02d", x32Track[0], x32N)
			if _, ok := ret.reaper[reaK]; ok {
				return nil, fmt.Errorf("duplicate mapping for %s", reaK)
			}
			if _, ok := ret.x32[x32K]; ok {
				return nil, fmt.Errorf("duplicate mapping for %s", x32K)
			}
			x32StatIndex := x32StatIndexBase + x32N - 1
			mapping := &mapping{x32Prefix: x32K, x32StatIndex: x32StatIndex, reaperPrefix: reaK, reaperTrackIndex: reaN}
			ret.reaper[reaK] = mapping
			ret.x32[x32K] = mapping
			ret.x32StatIndex[x32StatIndex] = mapping
		}
	}
	return &ret, nil
}

// parseRange inteprets the string r as either a single number, or a range of
// numbers (e.g. 4-7), and returns the low and high values in that range,
func parseRange(r string) (int32, int32, error) {
	rng := strings.Split(r, "-")
	switch l := len(rng); l {
	case 1:
		v, err := strconv.Atoi(rng[0])
		if err != nil {
			return -1, -1, fmt.Errorf("invalid range %v", err)
		}
		return int32(v), int32(v), nil
	case 2:
		lo, err := strconv.Atoi(rng[0])
		if err != nil {
			return -1, -1, fmt.Errorf("invalid range lower %v", err)
		}
		hi, err := strconv.Atoi(rng[1])
		if err != nil {
			return -1, -1, fmt.Errorf("invalid range upper %v", err)
		}
		if lo > hi {
			return -1, -1, fmt.Errorf("invalid range %d > %d", hi, lo)
		}

		return int32(lo), int32(hi), nil
	default:
		return -1, -1, fmt.Errorf("invalid range %q", r)
	}
}

// isreaperType returns true if the passed in mapping target is a valid Reaper track type.
func isReaperType(m string) error {
	if m != "track" && m != "master" {
		return fmt.Errorf("invalid reaper mapping type %s", m)
	}
	return nil
}
