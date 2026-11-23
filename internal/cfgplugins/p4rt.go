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
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openconfig/featureprofiles/internal/deviations"
	"github.com/openconfig/featureprofiles/internal/gnoi"
	"github.com/openconfig/featureprofiles/internal/helpers"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ondatra"
)

const (
	p4rtServerTimeout      = 2 * time.Minute
	p4rtServerPollInterval = 5 * time.Second
	aristaP4RTRunning      = "Server: running on port"
	aristaP4RTSection      = "p4-runtime"
	aristaGNMISection      = "management api gnmi"
)

var (
	p4rtMu sync.Mutex
	// p4rtFresh tracks DUTs whose P4RT server was started or restarted by this
	// test binary, so the election state is reset only once per binary.
	p4rtFresh = map[string]bool{}
)

// ConfigureP4RTServer ensures the P4RT server on the DUT is running with a
// clean election state before a test dials it. It is a no-op unless the DUT
// requires vendor pre-configuration. Configuration added by this function is
// removed when the calling test completes. It is safe to call from multiple
// tests in the same binary.
func ConfigureP4RTServer(t *testing.T, dut *ondatra.DUTDevice) {
	t.Helper()
	if !deviations.VendorPreConfigRequired(dut) {
		return
	}
	switch dut.Vendor() {
	case ondatra.ARISTA:
		configureAristaP4RTServer(t, dut)
	default:
		t.Logf("P4RT server pre-configuration is not implemented for vendor %s", dut.Vendor())
	}
}

func configureAristaP4RTServer(t *testing.T, dut *ondatra.DUTDevice) {
	t.Helper()
	p4rtMu.Lock()
	defer p4rtMu.Unlock()

	runningCfg := aristaCLIShow(t, dut, "show running-config")
	prevP4RT := aristaConfigBlock(runningCfg, aristaP4RTSection)
	running := aristaP4RTServerRunning(t, dut)
	if !running && aristaP4RTEnabled(prevP4RT) {
		awaitAristaP4RTServer(t, dut)
		running = true
	}

	if running {
		if !p4rtFresh[dut.Name()] {
			t.Log("Restarting P4Runtime to reset the P4RT election state")
			gnoi.KillProcess(t, dut, gnoi.P4RT, gnoi.SigTerm, true, true)
			awaitAristaP4RTServer(t, dut)
			p4rtFresh[dut.Name()] = true
		}
		return
	}

	sslProfile, vrf := aristaGNMITransport(t, runningCfg)
	var cfg strings.Builder
	if prevP4RT != nil {
		cfg.WriteString("no p4-runtime\n")
	}
	cfg.WriteString("p4-runtime\n transport grpc default\n")
	if sslProfile != "" {
		fmt.Fprintf(&cfg, " ssl profile %s\n", sslProfile)
	}
	if vrf != "" {
		fmt.Fprintf(&cfg, " vrf %s\n", vrf)
	}
	cfg.WriteString(" no shutdown\n")
	t.Logf("Enabling P4RT server with ssl profile %q and vrf %q", sslProfile, vrf)
	helpers.GnmiCLIConfig(t, dut, cfg.String())
	t.Cleanup(func() {
		p4rtMu.Lock()
		defer p4rtMu.Unlock()
		restore := "no p4-runtime\n" + strings.Join(prevP4RT, "\n")
		helpers.GnmiCLIConfig(t, dut, restore)
		delete(p4rtFresh, dut.Name())
	})
	awaitAristaP4RTServer(t, dut)
	p4rtFresh[dut.Name()] = true
}

// aristaGNMITransport returns the ssl profile and vrf of the gNMI transport so
// that the P4RT server is reachable the same way as gNMI. The "default"
// transport is preferred if several are configured.
func aristaGNMITransport(t *testing.T, runningCfg string) (sslProfile, vrf string) {
	t.Helper()
	block := aristaConfigBlock(runningCfg, aristaGNMISection)
	if block == nil {
		t.Fatalf("%q not found in running-config", aristaGNMISection)
	}
	type transport struct{ sslProfile, vrf string }
	var names []string
	transports := map[string]*transport{}
	var cur *transport
	for _, line := range block[1:] {
		fields := strings.Fields(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case len(fields) == 3 && fields[0] == "transport" && fields[1] == "grpc":
			cur = &transport{}
			names = append(names, fields[2])
			transports[fields[2]] = cur
		case cur == nil || indent <= 3:
			cur = nil
		case len(fields) == 3 && fields[0] == "ssl" && fields[1] == "profile":
			cur.sslProfile = fields[2]
		case len(fields) == 2 && fields[0] == "vrf":
			cur.vrf = fields[1]
		}
	}
	if len(names) == 0 {
		t.Fatalf("No gRPC transport found under %q in running-config", aristaGNMISection)
	}
	tr, ok := transports["default"]
	if !ok {
		tr = transports[names[0]]
	}
	return tr.sslProfile, tr.vrf
}

// aristaConfigBlock returns the lines of the top-level running-config section
// whose header is header, or nil if the section is not present.
func aristaConfigBlock(runningCfg, header string) []string {
	var block []string
	for _, line := range strings.Split(runningCfg, "\n") {
		line = strings.TrimRight(line, " \r")
		if block == nil {
			if line == header {
				block = []string{line}
			}
			continue
		}
		if !strings.HasPrefix(line, " ") {
			break
		}
		if strings.TrimSpace(line) != "!" {
			block = append(block, line)
		}
	}
	return block
}

func aristaP4RTEnabled(block []string) bool {
	for _, line := range block {
		if strings.TrimSpace(line) == "no shutdown" {
			return true
		}
	}
	return false
}

func aristaP4RTServerRunning(t *testing.T, dut *ondatra.DUTDevice) bool {
	t.Helper()
	out, err := aristaCLIShowE(t, dut, "show p4-runtime")
	return err == nil && strings.Contains(out, aristaP4RTRunning)
}

func awaitAristaP4RTServer(t *testing.T, dut *ondatra.DUTDevice) {
	t.Helper()
	for deadline := time.Now().Add(p4rtServerTimeout); !aristaP4RTServerRunning(t, dut); time.Sleep(p4rtServerPollInterval) {
		if time.Now().After(deadline) {
			t.Fatalf("P4RT server did not start within %v", p4rtServerTimeout)
		}
	}
	t.Log("P4RT server is running")
}

func aristaCLIShow(t *testing.T, dut *ondatra.DUTDevice, cmd string) string {
	t.Helper()
	out, err := aristaCLIShowE(t, dut, cmd)
	if err != nil {
		t.Fatalf("Failed to run %q: %v", cmd, err)
	}
	return out
}

func aristaCLIShowE(t *testing.T, dut *ondatra.DUTDevice, cmd string) (string, error) {
	t.Helper()
	resp, err := dut.RawAPIs().GNMI(t).Get(context.Background(), &gpb.GetRequest{
		Path:     []*gpb.Path{{Origin: "cli", Elem: []*gpb.PathElem{{Name: cmd}}}},
		Encoding: gpb.Encoding_ASCII,
	})
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for _, n := range resp.GetNotification() {
		for _, u := range n.GetUpdate() {
			out.WriteString(u.GetVal().GetAsciiVal())
		}
	}
	return out.String(), nil
}
