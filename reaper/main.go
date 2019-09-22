package main

import (
	"encoding/json"
	"flag"
	"github.com/9600org/x32"
	"github.com/golang/glog"
	"io/ioutil"
)

var (
	config = flag.String("config", "proxy.config", "Reaper Proxy config file")
)

func main() {
	flag.Parse()
	flag.Set("logtostderr", "true")

	confRaw, err := ioutil.ReadFile(*config)
	if err != nil {
		glog.Exitf("Failed to read config file: %s", err)
	}
	config := x32.ProxyConfig{}
	if err := json.Unmarshal([]byte(confRaw), &config); err != nil {
		glog.Exitf("Couldn't parse config file: %s", err)
	}

	glog.Info("Starting X32 Reaper server")

	proxy, err := x32.NewProxy(config)
	if err != nil {
		glog.Exitf("Failed to create proxy: %q", err)
	}

	glog.Exitf("Server exiting: %s", proxy.ListenAndServe())
}
