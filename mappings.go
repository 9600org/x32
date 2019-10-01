package x32

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/9600org/go-osc/osc"
	"github.com/golang/glog"
)

// targetTransform knows how to translate an OSC message from one device into
// zero or more OSC messages intended for a different device.
//
// This is the core of the X32<->Reaper proxy.
//
// There is one targetTransform instance for each OSC message address that
// we're interested in.
type targetTransform struct {
	// target is the destination OSC Address.
	// TODO: remove this?
	target string
	// transform is the function which actually does the work of transforming its
	// input message into zero or more output messages.
	transform func(*targetTransform, *mapping, osc.Message) ([]osc.Message, error)
	// state is a pointer to the current proxy state, this is where transform
	// functions can find out about track mappings, fx mappings, etc.
	state *state
	// nameHints contains the set of icon/colour hint matchers
	nameHints []nameHint
	fxIndex   int32
}

// Apply applies the transform to the passde in message, and returns the set of
// output messages to be sent.
func (tt *targetTransform) Apply(m *mapping, msg osc.Message) ([]osc.Message, error) {
	return tt.transform(tt, m, msg)
}

var (
	// reaperX32StripMap is a map of all /track/${ID}/... subaddresses which
	// are sent by Reaper, and their corresponding X32 targets.
	reaperX32StripMap = map[string]targetTransform{
		"volume": targetTransform{target: "mix/fader",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"mute": targetTransform{target: "mix/on",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				f, ok := msg.Arguments[0].(float32)
				if !ok {
					return nil, fmt.Errorf("argument 0 is a %T, expected float32", msg.Arguments[0])
				}
				ni, err := NotInt(interface{}(int32(f)))
				if err != nil {
					return nil, err
				}
				msg.Arguments[0] = ni
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"pan": targetTransform{target: "mix/pan",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"select": targetTransform{target: "-stat/selidx",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				eq, err := isArgEq(msg, 0, float32(0))
				if err != nil {
					return nil, err
				}
				if eq {
					return []osc.Message{}, nil
				}
				defer func() {
					tt.state.selectTrackX32StatID(m.x32StatIndex)
				}()
				msgs := []osc.Message{
					// select track on X32
					osc.Message{
						Address:   fmt.Sprintf("/%s", tt.target),
						Arguments: []interface{}{int32(m.x32StatIndex)},
					},
					// Ping reaper to send FX info
					//TODO: broken - this send to x32...
					/*
						osc.Message{
							Address: fmt.Sprintf("/device/track/select"),
							Arguments: []interface{}{int32(m.reaperTrackIndex)},
						},
						osc.Message{Address: "/action/41743", Arguments: []interface{}{int32(1)}},
					*/
				}
				return msgs, nil
			},
		},
		"solo": targetTransform{target: "-stat/solosw",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				if l := len(msg.Arguments); l != 1 {
					return nil, fmt.Errorf("got %d arguments, expected 1", l)
				}
				f, ok := msg.Arguments[0].(float32)
				if !ok {
					return nil, fmt.Errorf("got %T arg, expected float32", msg.Arguments[0])
				}
				msg.Arguments[0] = int32(f)
				msg.Address = fmt.Sprintf("/%s/%02d", tt.target, m.x32StatIndex)
				return []osc.Message{msg}, nil
			},
		},
		"name": targetTransform{target: "config/name",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				name, ok := msg.Arguments[0].(string)
				if !ok {
					return nil, fmt.Errorf("got %T arg, expected string", msg.Arguments[0])
				}

				r := make([]osc.Message, 0, 3)

				colID, iconID := guessColourAndIcon(tt.nameHints, name)
				if colID > -1 {
					r = append(r, osc.Message{
						Address:   fmt.Sprintf("/%s/%s", m.x32Prefix, "config/color"),
						Arguments: []interface{}{colID},
					})
				}
				if iconID > -1 {
					r = append(r, osc.Message{
						Address:   fmt.Sprintf("/%s/%s", m.x32Prefix, "config/icon"),
						Arguments: []interface{}{iconID},
					})
				}

				// pass on name setting
				r = append(r,
					osc.Message{
						Address:   fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target),
						Arguments: msg.Arguments,
					})

				// do guessing thing
				return r, nil
			},
		},
	}

	// reaperX32StripFXMap contains mappings for reaper FX messages
	reaperX32StripFXMap = map[string]targetTransform{
		// General purpose EQ plugins
		// TODO fx index management
		/*
			"fx/1/fxparam/%d/bypass": targetTransform{target: "fx/%d/eq/on",
				transform: func(tt *targetTransform, m mapping, msg osc.Message) ([]osc.Message, error) {
					if l := len(msg.Arguments); l != 1 {
						return nil, fmt.Errorf("%s: got %d arguments, expected 1", tt.target, l)
					}
					i, ok := msg.Arguments[0].(int32)
					if !ok {
						return nil, fmt.Errorf("%s: got %T argument, expected int32", tt.target, i)
					}
					msg.Arguments[0] = i ^ 1
					msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
		*/
		"fx/%d/fxparam/%d/value": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				f, err := getFloatArg(msg, 0)
				if err != nil {
					return nil, err
				}
				if tt.state.selectedTrack == nil {
					return nil, fmt.Errorf("got param message but no track is currently selected")
				}
				fxMap := m.fxMap
				if fxMap == nil {
					glog.V(2).Infof("fxmap nil - probably haven't seen param names yet to create map")
					return nil, nil

				}
				if fxMap.plugParams == nil {
					return nil, fmt.Errorf("plugParams nil")
				}

				//TODO broken for different plugs handling different fx
				//		msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				bits := strings.Split(msg.Address, "/")
				if l := len(bits); l != 8 {
					return nil, fmt.Errorf("got address with %d parts, expected 8", l)
				}
				paramIndex, err := strconv.Atoi(bits[6])
				if err != nil {
					return nil, err
				}
				paramInfo, ok := fxMap.getEqParamInfo(int32(paramIndex))
				if !ok {
					return nil, nil //fmt.Errorf("paramInfo @ %d nil", paramIndex)
				}

				norm := paramInfo.plugToNorm(f)
				msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, paramInfo.x32AddrFormat)
				x32v := paramInfo.normToX32(norm)
				var x32Arg interface{}
				if paramInfo.format != nil {
					x32Arg = paramInfo.format(x32v)
				} else {
					x32Arg = x32v
				}
				msg.Arguments = []interface{}{x32Arg}
				return []osc.Message{msg}, nil
			},
		},
		/*
				// TODO: ReaEQ
			"fxeq/band/%d/bypass": targetTransform{target: "fx/%d/eq/on",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					if l := len(msg.Arguments); l != 1 {
						return nil, fmt.Errorf("%s: got %d arguments, expected 1", tt.target, l)
					}
					i, ok := msg.Arguments[0].(int32)
					if !ok {
						return nil, fmt.Errorf("%s: got %T argument, expected int32", tt.target, i)
					}
					msg.Arguments[0] = i ^ 1
					msg.Address = fmt.Sprintf("/%s/%s", m.x32Prefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
			"fxeq/band/%d/type": targetTransform{target: "eq/%d/type",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
			"fxeq/band/%d/q/oct": targetTransform{target: "eq/%d/q",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
			"fxeq/band/%d/f/hz": targetTransform{target: "eq/%d/f",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
			"fxeq/band/%d/g/db": targetTransform{target: "eq/%d/g",
				transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
					return []osc.Message{msg}, nil
				},
			},
		*/
	}

	// x32eaperStripMap is a map of all addresses which
	// are sent by Reaper, and their corresponding X32 targets.
	x32ReaperStripMap = map[string]targetTransform{
		"mix/fader": targetTransform{target: "volume",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"mix/on": targetTransform{target: "mute",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				i, err := NotInt(msg.Arguments[0])
				if err != nil {
					return nil, err
				}
				f, err := IntToFloat(i)
				if err != nil {
					return nil, err
				}
				msg.Arguments[0] = f
				if m.reaperPrefix != "master" {
					msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				} else {
					msg.Address = "/action/18"
					msg.Arguments[0] = i
				}
				return []osc.Message{msg}, nil
			},
		},
		"mix/pan": targetTransform{target: "pan",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
	}

	// x32ReaperStripFXMap contains mappings for x32 FX messages
	x32ReaperStripFXMap = map[string]targetTransform{
		"eq/%d/on": targetTransform{target: "fxeq/band/%d/bypass",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				if l := len(msg.Arguments); l != 1 {
					return nil, fmt.Errorf("%s: got %d arguments, expected 1", tt.target, l)
				}
				i, ok := msg.Arguments[0].(int32)
				if !ok {
					return nil, fmt.Errorf("%s: got %T argument, expected int32", tt.target, i)
				}
				msg.Arguments[0] = i ^ 1
				msg.Address = fmt.Sprintf("/%s/%s", m.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
		"eq/%d/type": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				if m.fxMap == nil {
					return nil, fmt.Errorf("fxmap nil")

				}
				if m.fxMap.plugParams == nil {
					return nil, fmt.Errorf("plugParams nil")
				}
				x32EqType, err := getIntArg(msg, 0)
				if err != nil {
					return nil, err
				}

				msg.Address = fmt.Sprintf("/%s/fx/%d/fxparam/%d/value", m.reaperPrefix, m.fxMap.reaEqIndex, m.fxMap.plugParams.eqTypeBandParam[tt.fxIndex])
				msg.Arguments = []interface{}{m.fxMap.plugParams.eqTypeToPlug(float32(x32EqType))}
				return []osc.Message{msg}, nil
			},
		},
		"eq/%d/q": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				f, err := getFloatArg(msg, 0)
				if err != nil {
					return nil, err
				}
				msg.Address = fmt.Sprintf("/%s/fx/%d/fxparam/%d/value", m.reaperPrefix, m.fxMap.reaEqIndex, m.fxMap.plugParams.eqQBandParam[tt.fxIndex])
				msg.Arguments = []interface{}{m.fxMap.plugParams.eqQToPlug(x32QLogToOct(f))}
				return []osc.Message{msg}, nil
			},
		},
		"eq/%d/f": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				f, err := getFloatArg(msg, 0)
				if err != nil {
					return nil, err
				}
				msg.Address = fmt.Sprintf("/%s/fx/%d/fxparam/%d/value", m.reaperPrefix, m.fxMap.reaEqIndex, m.fxMap.plugParams.eqFreqBandParam[tt.fxIndex])
				msg.Arguments = []interface{}{m.fxMap.plugParams.eqFreqToPlug(x32EqFreqLogToHz(f))}
				return []osc.Message{msg}, nil
			},
		},
		"eq/%d/g": targetTransform{
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				fxMap := m.fxMap
				if fxMap == nil {
					return nil, nil
				}
				f, err := getFloatArg(msg, 0)
				if err != nil {
					return nil, err
				}
				msg.Address = fmt.Sprintf("/%s/fx/%d/fxparam/%d/value", m.reaperPrefix, fxMap.reaEqIndex, fxMap.plugParams.eqGainBandParam[tt.fxIndex])
				msg.Arguments = []interface{}{fxMap.plugParams.eqGainToPlug(f)}
				return []osc.Message{msg}, nil
			},
		},
	}

	x32ReaperStatMap = map[string]targetTransform{
		"-stat/solosw": targetTransform{target: "solo",
			transform: func(tt *targetTransform, m *mapping, msg osc.Message) ([]osc.Message, error) {
				if l := len(msg.Arguments); l != 1 {
					return nil, fmt.Errorf("unexpected number of arguments (%d), expected 1", l)
				}
				i, ok := msg.Arguments[0].(int32)
				if !ok {
					return nil, fmt.Errorf("got unexpected argument type %T", msg.Arguments[0])
				}
				bits := strings.Split(msg.Address, "/")
				if l := len(bits); l != 4 {
					return nil, fmt.Errorf("unexpected address format (%s), found %d parts", msg.Address, l)
				}
				statID, err := strconv.Atoi(bits[3])
				if err != nil {
					return nil, fmt.Errorf("failed to parse statID from address[2] (%s): %s", msg.Address, err)
				}
				statID--
				msg.Arguments[0] = float32(i)
				reaperMapping, ok := tt.state.getTrackMappingForX32StatID(int32(statID))
				if !ok {
					return nil, fmt.Errorf("no stat<>mapping found for statID %d", statID)
				}
				msg.Address = fmt.Sprintf("/%s/%s", reaperMapping.reaperPrefix, tt.target)
				return []osc.Message{msg}, nil
			},
		},
	}

	x32ReaperFanoutStatMap = map[string]targetTransform{
		"-stat/selidx": targetTransform{target: "select",
			transform: func(tt *targetTransform, _ *mapping, msg osc.Message) ([]osc.Message, error) {
				id, ok := msg.Arguments[0].(int32)
				if !ok {
					return []osc.Message{}, fmt.Errorf("got non-int32 fanout arg[0] of type %T", msg.Arguments[0])
				}
				mapping, ok := tt.state.getTrackMappingForX32StatID(id)
				if !ok {
					glog.Errorf("ignoring selidx for unmapped id %d", id)
					return []osc.Message{}, nil
				}
				defer func() {
					tt.state.selectTrackX32StatID(id)
				}()
				msgs := []osc.Message{
					// unselect all repaer tracks:
					osc.Message{Address: fmt.Sprintf("/action/40297")},
					// select track matching x32:
					osc.Message{Address: fmt.Sprintf("/%s/%s", mapping.reaperPrefix, tt.target), Arguments: []interface{}{int32(1)}},
					// Ping reaper to send FX info
					osc.Message{Address: "/device/track/select", Arguments: []interface{}{mapping.reaperTrackIndex}},
					osc.Message{Address: "/device/fx/select", Arguments: []interface{}{int32(1)}},
					//osc.Message{Address: "/action/41743", Arguments: []interface{}{int32(1)}},
				}
				return msgs, nil
			},
		},
	}
)

//x32 trackids
// ch 1-32
// auxin 33-48
// bus 49-64
// matrix (1-6) 65-70
// Main mid 71
// Main LR 72
// DCA 73-82
//
//
//  /track/../mute <-> /{type}/mix/on (NOT)
//  /track/../pan <-> /{type}/mix/pan
//  /track/../volume <-> /{type}/mix/fader
//  /track/../select <-> /-stat/selidx
//  /track/../solo <-> /-stat/solosw

// FX:
// /ch/01/eq/1/type i
// /ch/01/eq/1/g f
// /ch/01/eq/1/q f
// /ch/01/eq/1/f f
