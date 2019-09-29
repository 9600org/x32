package x32

import (
	"github.com/golang/glog"
	"math"
)

func x32LogToHz(l float32) float32 {

	Hz := math.Exp(2.9957322738 + float64(l)*6.9077552785)
	r := float32((((math.Log(Hz+200.0) - 3.0099999) / 1.6) - 1.56) / 2.75)
	glog.Infof("%f -> %f", Hz, r)
	return r
}
