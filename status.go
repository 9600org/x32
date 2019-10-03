package x32

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/golang/glog"
)

func ListenAndServeStatus(listenAddr string, s *state) error {
	const (
		master = `
		<html>
		<body>
		Track Mappings:
		<table>
		{{range $k, $v := .}}
			<tr>
					<td>{{$k}}</td>
					<td>{{$v | printf "%#v"}}</td>
			</tr>
		{{end}}
		</table>
		</body>
		</html>`
	)

	funcs := template.FuncMap{
		"join": strings.Join,
	}
	masterTmpl, err := template.New("master").Funcs(funcs).Parse(master)
	if err != nil {
		glog.Fatal(err)
		return err
	}

	http.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if err := masterTmpl.Execute(w, s.trackMap.reaper); err != nil {
			glog.Fatal(err)
		}
	})

	return http.ListenAndServe(listenAddr, nil)
}
