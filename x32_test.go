package x32

import (
	"testing"
)

func TestSplitAddress(t *testing.T) {
	for _, test := range []struct {
		desc     string
		address  string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{
			desc:     "Valid",
			address:  "host:1234",
			wantHost: "host",
			wantPort: 1234,
		}, {
			desc:    "InvalidPort",
			address: "host:banana",
			wantErr: true,
		}, {
			desc:     "NoHost",
			address:  ":4567",
			wantHost: "",
			wantPort: 4567,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			gotHost, gotPort, gotErr := splitAddress(test.address)
			if test.wantErr && gotErr != nil {
				return
			}
			if test.wantErr && gotErr == nil {
				t.Fatal("got no error, wanted error")
			}
			if gotErr != nil {
				t.Fatalf("got %v, want no error", gotErr)
			}
			if gotHost != test.wantHost {
				t.Errorf("got host %s, want %s", gotHost, test.wantHost)
			}
			if gotPort != test.wantPort {
				t.Errorf("got port %d, want %d", gotPort, test.wantPort)
			}
		})
	}
}

func TestParrseRange(t *testing.T) {
	for _, test := range []struct {
		desc    string
		r       string
		wantErr bool
		wantLo  int32
		wantHi  int32
	}{
		{
			desc:   "ValidSingle",
			r:      "56",
			wantLo: 56,
			wantHi: 56,
		}, {
			desc:   "ValidRange",
			r:      "23-45",
			wantLo: 23,
			wantHi: 45,
		}, {
			desc:    "InvalidReversed",
			r:       "6-2",
			wantErr: true,
		}, {
			desc:    "InvalidIncomplete",
			r:       "6-",
			wantErr: true,
		}, {
			desc:    "InvalidGarbage",
			r:       "Hello!",
			wantErr: true,
		},
	} {
		t.Run(test.desc, func(t *testing.T) {
			gotLo, gotHi, gotErr := parseRange(test.r)
			if test.wantErr && gotErr != nil {
				return
			}
			if test.wantErr && gotErr == nil {
				t.Fatalf("Got no error, wanted error")
			}
			if gotErr != nil {
				t.Fatalf("Got unexpected error %s", gotErr)
			}
			if gotLo != test.wantLo || gotHi != test.wantHi {
				t.Fatalf("Got values %d,%d want %d,%d", gotLo, gotHi, test.wantLo, test.wantHi)
			}
		})
	}
}

func TestIsReaperType(t *testing.T) {
	if gotErr := isReaperType("track"); gotErr != nil {
		t.Fatalf("got unexpected error %s, want no error", gotErr)
	}

	if gotErr := isReaperType("ch"); gotErr == nil {
		t.Fatal("got no error, want error")
	}
}

