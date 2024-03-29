package x32

import (
	"math"

	"github.com/golang/glog"
)

// x32LogToHz returns the frquency in Hz which coresponds to the 0..1 float
// value sent by the X32 when the Eq Frequency knob is turned.
func x32EqFreqLogToHz(f float32) float32 {
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}
	// =EXP(2.9957322738+A1*6.9077552785)
	Hz := float32(math.Exp(2.9957322738 + float64(f)*6.9077552785))
	return Hz
}

// hzToX32 returns the log float 0..1 value corresponding to the passed in
// frequency.
// The frequency should be in the range 20..20000. Values outside this range
// will be treated as though they were at the corresponding range limit.
func hzToX32EqFreq(hz float32) float32 {
	if hz < 20 {
		hz = 20
	} else if hz > 20000 {
		hz = 20000
	}

	f := float32((math.Log10(float64(hz)) - 1.30102999622) / 3.0)
	return f
}

// hzToNeutronEqLog converts a frequency in Hz to the corresponding log value
// used by the Neutron eq OSC params.
func hzToNeutronEqLog(hz float32) float32 {
	if hz < 20 {
		hz = 20
	} else if hz > 20000 {
		hz = 20000
	}

	f := float32((math.Atanh((float64(hz)-18.99300151)/200000000-1) - -9.9) / 4.947806)
	return f
}

// neutronEqLogToHz converts the Neutron eq log frequency OSC value to the
// corresponding frequency in Hz.
func neutronEqLogToHz(f float32) float32 {
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}

	Hz := float32(18.99300151 + (1.0+math.Tanh(-9.9+float64(f)*4.9478063))*200000000)
	return Hz
}

// x32QLogToOct converts an X32 Eq Q OSC log value to the corresponding Q
// octave value.
func x32QLogToOct(f float32) float32 {
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}

	oct := float32(math.Exp(2.302585093 - float64(f)*3.5))
	return oct
}

// octToX32Q converts a Q octave value to the log float representation used by
// the X32 Eq Q OSC param.
func octToX32Q(o float32) float32 {
	if o > 10 {
		o = 10
	} else if o < 0.3 {
		o = 0.3
	}

	f := float32(-(math.Log10(float64(o)) - 1.0) / 1.520030687)
	return f
}

// neutronQLogToOct converts from the Neutron Q float OSC param value to the
// corresponding Q octave value.
func neutronQLogToOct(f float32) float32 {
	if f < 0 {
		f = 0
	} else if f > 1 {
		f = 1
	}

	q := float32(-0.9022102697 + (1.0+math.Tanh(-4.1+float64(f)*1.86))*1825)
	return q
}

// octToNeutronQLog converts from Q octave width to log float representation
// used by Neutron OSC param.
func octToNeutronQLog(o float32) float32 {
	if o < 0.1 {
		o = 0.1
	} else if o > 40 {
		o = 40
	}

	f := float32((math.Atanh(((float64(o)+0.9022102697)/1825.0)-1.0) + 4.1) / 1.86)
	return f
}

func normToNeutronGain(g float32) float32 {
	return g + 0.167
}

func neutronToNormGain(g float32) float32 {
	return g - 0.167
}

const (
	NeutronEqProp = iota
	NeutronEqBell
	NeutronEqBandShelf
	NeutronEqAnalogLowLowShelf
	NeutronEqBaxLowShelf
	NeutronEqVintageLowShelf
	NeutronEqAnalogHighLowShelf
	NeutronEqBaxHighShelf
	NeutronEqVintageHighShelf
	NeutronEqFlatLowPass
	NeutronEqResonantLowPass
	NeutronEqFlatHighPass
	NeutronEqResonantHighPass
	NeutronEqMax float32 = 12.0
)

func x32EqTypeToNeutron(t float32) float32 {
	// TODO: make these mappings be configurable
	switch t {
	case 0:
		return NeutronEqFlatHighPass / NeutronEqMax
	case 1:
		return NeutronEqVintageLowShelf / NeutronEqMax
	case 2:
		return NeutronEqProp / NeutronEqMax
	case 3:
		return NeutronEqBandShelf / NeutronEqMax
	case 4:
		return NeutronEqVintageHighShelf / NeutronEqMax
	case 5:
		return NeutronEqFlatLowPass / NeutronEqMax
	}
	glog.Errorf("Got unknown x32EqType %f", t)
	return 0
}

func neutronEqTypeToX32(t float32) float32 {
	// TODO: make these mappings be configurable
	i := int(t * NeutronEqMax)
	switch i {
	case NeutronEqFlatHighPass:
		return 0
	case NeutronEqVintageLowShelf:
		return 1
	case NeutronEqProp:
		return 2
	case NeutronEqBandShelf:
		return 3
	case NeutronEqVintageHighShelf:
		return 4
	case NeutronEqFlatLowPass:
		return 5
	case NeutronEqResonantHighPass, NeutronEqResonantLowPass, NeutronEqAnalogLowLowShelf, NeutronEqBaxLowShelf, NeutronEqBell, NeutronEqBaxHighShelf:
		return 0
	}
	glog.Errorf("Got unknown x32EqType %d", i)
	return 0
}
