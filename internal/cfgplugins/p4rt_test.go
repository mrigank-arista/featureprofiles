// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cfgplugins

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

const testRunningConfig = `! Command: show running-config
hostname dut
!
management api gnmi
   transport grpc other
      ssl profile OTHER
   transport grpc default
      ssl profile SELFSIGNED
      vrf mgmt
      authorization requests
   provider eos-native
!
p4-runtime
   transport grpc default
      ssl profile SELFSIGNED
      vrf mgmt
   no shutdown
!
end
`

func TestAristaConfigBlock(t *testing.T) {
	tests := []struct {
		desc   string
		header string
		want   []string
	}{{
		desc:   "p4-runtime",
		header: "p4-runtime",
		want: []string{
			"p4-runtime",
			"   transport grpc default",
			"      ssl profile SELFSIGNED",
			"      vrf mgmt",
			"   no shutdown",
		},
	}, {
		desc:   "missing",
		header: "management api gribi",
	}}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got := aristaConfigBlock(testRunningConfig, tc.header)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("aristaConfigBlock(%q) diff (-want +got):\n%s", tc.header, diff)
			}
		})
	}
	if !aristaP4RTEnabled(aristaConfigBlock(testRunningConfig, aristaP4RTSection)) {
		t.Errorf("aristaP4RTEnabled() = false, want true")
	}
}

func TestAristaGNMITransport(t *testing.T) {
	tests := []struct {
		desc           string
		cfg            string
		wantSSLProfile string
		wantVRF        string
	}{{
		desc:           "prefer default transport",
		cfg:            testRunningConfig,
		wantSSLProfile: "SELFSIGNED",
		wantVRF:        "mgmt",
	}, {
		desc:           "first transport",
		cfg:            "management api gnmi\n   transport grpc foo\n      vrf mgmt\n!\n",
		wantSSLProfile: "",
		wantVRF:        "mgmt",
	}}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			gotSSLProfile, gotVRF := aristaGNMITransport(t, tc.cfg)
			if gotSSLProfile != tc.wantSSLProfile || gotVRF != tc.wantVRF {
				t.Errorf("aristaGNMITransport() = (%q, %q), want (%q, %q)", gotSSLProfile, gotVRF, tc.wantSSLProfile, tc.wantVRF)
			}
		})
	}
}
