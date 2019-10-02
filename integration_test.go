package x32_test

import (
	"testing"

	"github.com/9600org/x32"
)

var testConfig = x32.ProxyConfig{
	ListenAddress: "localhost:0",
	ReaperAddress: "localhost:0",
	X32Address:    "localhost:0",

	Mapping: map[string]string{
		"master":      "main/st",
		"track/3":     "ch/1",
		"track/14-16": "bus/4-6",
	},
}

func TestNewProxy(t *testing.T) {
	_, err := x32.NewProxy(testConfig)
	if err != nil {
		t.Fatalf("NewProxy = %v, expected no error", err)
	}
}
