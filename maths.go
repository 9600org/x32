package x32

import (
	"math"
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

	f := float32((math.Log(float64(hz)) - 1.30102999622) / 3.0)
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

	f := float32(-(math.Log(float64(o)) - 1.0) / 1.520030687)
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
