package x32

import (
	"sync"
)

// normalisationFunc is a transfer function for converting fx param values.
type normalisationFunc func(float32) float32

// paramInfo represents a VST fx parameter.
type paramInfo struct {
	// x32AddrFormat is a format string for the X32 address suffix.
	x32AddrFormat string
	// normToX32 converts an fx parameter value to its X32 form,
	normToX32 normalisationFunc
	// plugToNorm converts a VST fx parameter value to its normalised form.
	plugToNorm normalisationFunc
	// format is a function which can perform the necessary type conversions on
	// the fx parameter value.
	format func(interface{}) interface{}
}

// plugParams describes the set of VST plugin parameters which correspond to
// the X32 controls available.
type plugParams struct {
	// plugName is the VST name.
	plugName string

	// EQ X32 band -> Param indices.
	// VST parameter indices are the parameter number N in Reaper OSC addresses:
	//    /fx/./fxparam/N/value
	// The values in the arrays below correspond to the zero-based X32 eq bands.

	// eqTypeBandParam holds the eq band type parameters corresponding to the X32 eq bands
	eqTypeBandParam []int32
	// eqFreqBandParam holds the eq band frequency parameters corresponding to the X32 eq bands
	eqFreqBandParam []int32
	// eqGainBandParam holds the eq band gain parameters corresponding to the X32 eq bands
	eqGainBandParam []int32
	// eqQBandParam holds the eq band Q parameters corresponding to the X32 eq bands
	eqQBandParam []int32
	// eqEnableBandParam holds the eq band enable parameters corresponding to the X32 eq bands
	eqEnableBandParam []int32
	// EQ plug Param indices -> X32 band/fx info
	eqParamInfo map[int32]paramInfo

	// TODO eqTypeMap

	// eqFreqToPlug converts from Hz to the VST plugin representation for that frequency.
	eqFreqToPlug normalisationFunc
	// eqFreqFromPlug converts from the VST plugin representation of frequency to Hz.
	eqFreqFromPlug normalisationFunc
	// eqGainToPlug converts from dB to the VST plugin representation for that gain.
	eqGainToPlug normalisationFunc
	// eqGainFromPlug converts from the VST plugin representation of gain to dB.
	eqGainFromPlug normalisationFunc
	// eqQToPlug converts from octaves to the VST plugin representation for that Q.
	eqQToPlug normalisationFunc
	// eqQFromPlug converts from the VST plugin representation of Q to octaves.
	eqQFromPlug normalisationFunc
	// eqTypeToPlug converts from X32 eq type to the VST plugin representation for that type.
	eqTypeToPlug normalisationFunc
	// eqTypeFromPlug converts from the VST plugin representation of eq type to X32 id.
	eqTypeFromPlug normalisationFunc
}

// fxMap represents the VST<->X32 mapping of effects.
type fxMap struct {
	// mu protects the fields below.
	mu sync.RWMutex

	// FX indices:

	// reaEqIndex is the VST plug index on the reaper track which handles EQ
	reaEqIndex int32
	// reaEqIndex is the VST plug index on the reaper track which handles Gate
	reaGateIndex int32
	// reaEqIndex is the VST plug index on the reaper track which handles Compression
	reaDynIndex int32

	// plugParams tracks the VST parameter indices
	plugParams *plugParams

	// Gate Param indices

	// Dyn Param indices
}

func (fx *fxMap) setEqPlugBandFreqParam(x32Band, fxParam int32) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.plugParams.eqFreqBandParam[x32Band] = fxParam
}
func (fx *fxMap) getEqPlugBandFreqParam(x32Band int32) int32 {
	fx.mu.RLock()
	defer fx.mu.RUnlock()
	return fx.plugParams.eqFreqBandParam[x32Band]
}
func (fx *fxMap) setEqPlugBandGainParam(x32Band, fxParam int32) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.plugParams.eqGainBandParam[x32Band] = fxParam
}
func (fx *fxMap) getEqPlugBandGainParam(x32Band int32) int32 {
	fx.mu.RLock()
	defer fx.mu.RUnlock()
	return fx.plugParams.eqGainBandParam[x32Band]
}
func (fx *fxMap) setEqPlugBandTypeParam(x32Band, fxParam int32) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.plugParams.eqTypeBandParam[x32Band] = fxParam
}
func (fx *fxMap) getEqPlugBandTypeParam(x32Band int32) int32 {
	fx.mu.RLock()
	defer fx.mu.RUnlock()
	return fx.plugParams.eqTypeBandParam[x32Band]
}
func (fx *fxMap) setEqPlugBandQParam(x32Band, fxParam int32) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.plugParams.eqQBandParam[x32Band] = fxParam
}
func (fx *fxMap) getEqPlugBandQParam(x32Band int32) int32 {
	fx.mu.RLock()
	defer fx.mu.RUnlock()
	return fx.plugParams.eqQBandParam[x32Band]
}
func (fx *fxMap) setEqParamInfo(fxParam int32, info paramInfo) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.plugParams.eqParamInfo[fxParam] = info
}
func (fx *fxMap) getEqParamInfo(fxParam int32) (paramInfo, bool) {
	fx.mu.RLock()
	defer fx.mu.RUnlock()
	pi, ok := fx.plugParams.eqParamInfo[fxParam]
	return pi, ok
}
