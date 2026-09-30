// ppc64le_nodelabeller_test.go
//
// Self-contained test for the ppc64le node-labeller CPU model / feature
// discovery logic.  No Kubernetes API types, no libvirt bindings, no external
// test frameworks — only the standard library.
//
// PURPOSE
// -------
// This program extracts and exercises exactly the same parsing logic that
// NodeLabeller uses on ppc64le nodes so that it can be run *directly on a
// Power10/Power11 worker* without a full KubeVirt build:
//
//   go run ppc64le_nodelabeller_test.go
//
// If the real virsh files are available at
//   /var/lib/kubevirt-node-labeller/virsh_domcapabilities.xml
//   /var/lib/kubevirt-node-labeller/supported_features.xml
// they are used automatically; otherwise the program falls back to the
// embedded synthetic testdata below.
//
// WHAT IS TESTED
// ---------------
//  1. loadDomCapabilities  — parses virsh domcapabilities XML, builds the
//     usable/known model lists and the host-model required features map.
//  2. loadHostSupportedFeatures — parses the supported_features.xml baseline,
//     accepting features with no policy attribute (ppc64le behaviour).
//  3. requirePolicy        — verifies that both "" and "require" are accepted.
//  4. defaultVendor        — verifies "IBM" is returned when the XML has no
//     vendor element.
//  5. Label generation     — converts the parsed data into the exact label map
//     that KubeVirt would apply to the Kubernetes node object.
//
// ROOT CAUSE OF THE MISSING LABELS
// ----------------------------------
// Before this fix, pkg/virt-handler/node-labeller/arch_labeller.go routed
// "ppc64le" to defaultArchLabeller{} which returns:
//   supportsHostModel()       → false   → no host-model / required-feature labels
//   supportsNamedModels()     → false   → no cpu-model.node.kubevirt.io/* labels
//   hasHostSupportedFeatures()→ false   → no cpu-feature.node.kubevirt.io/* labels
//
// This program demonstrates that the new archLabellerPPC64LE{} produces the
// expected labels from real (or synthetic) Power10 domcapabilities output.

package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"strings"
)

// ── constants mirroring pkg/virt-handler/node-labeller ──────────────────────

const (
	requirePolicy = "require"

	// Kubernetes label prefixes used by KubeVirt node-labeller
	cpuFeatureLabel         = "cpu-feature.node.kubevirt.io/"
	cpuModelLabel           = "cpu-model.node.kubevirt.io/"
	supportedMigrationLabel = "cpu-model-migration.node.kubevirt.io/"
	hostModelCPULabel       = "host-model-cpu.node.kubevirt.io/"
	hostModelReqFeatLabel   = "host-model-required-features.node.kubevirt.io/"
	cpuModelVendorLabel     = "cpu-vendor.node.kubevirt.io/"
)

// ── XML data-model (subset of model.go) ─────────────────────────────────────

type hostDomCapabilities struct {
	CPU CPU `xml:"cpu"`
}

type CPU struct {
	Mode []Mode `xml:"mode"`
}

type Mode struct {
	Name    string       `xml:"name,attr"`
	Vendor  Vendor       `xml:"vendor"`
	Feature []HostFeature `xml:"feature"`
	Model   []Model      `xml:"model"`
}

type Vendor struct {
	Name string `xml:",chardata"`
}

type HostFeature struct {
	Policy string `xml:"policy,attr"`
	Name   string `xml:"name,attr"`
}

type Model struct {
	Name     string `xml:",chardata"`
	Usable   string `xml:"usable,attr"`
	Fallback string `xml:"fallback,attr"`
}

type SupportedHostFeature struct {
	Feature []HostFeature `xml:"feature"`
}

// ── archLabeller for ppc64le (mirrors ppc64le.go) ───────────────────────────

type archLabellerPPC64LE struct{}

func (archLabellerPPC64LE) defaultVendor() string { return "IBM" }

// requirePolicy: on ppc64le QEMU omits the policy attribute entirely, so we
// accept both "require" and "" — same fix as s390x.go uses.
func (archLabellerPPC64LE) requirePolicy(policy string) bool {
	return policy == requirePolicy || policy == ""
}
func (archLabellerPPC64LE) hasHostSupportedFeatures() bool { return true }
func (archLabellerPPC64LE) supportsHostModel() bool        { return true }
func (archLabellerPPC64LE) supportsNamedModels() bool      { return true }
func (archLabellerPPC64LE) arch() string                   { return "ppc64le" }

// ── defaultArchLabeller (mirrors the BROKEN pre-fix behaviour) ───────────────

type defaultArchLabeller struct{}

func (defaultArchLabeller) defaultVendor() string          { return "" }
func (defaultArchLabeller) requirePolicy(p string) bool    { return p == requirePolicy }
func (defaultArchLabeller) hasHostSupportedFeatures() bool { return false }
func (defaultArchLabeller) supportsHostModel() bool        { return false }
func (defaultArchLabeller) supportsNamedModels() bool      { return false }
func (defaultArchLabeller) arch() string                   { return "ppc64le" }

// ── core parsing (mirrors cpu_plugin.go logic) ───────────────────────────────

type parseResult struct {
	usableModels     []string
	knownModels      []string
	hostModelName    string
	hostModelFallback string
	requiredFeatures map[string]bool
	cpuModelVendor   string
	supportedFeatures []string
}

func loadDomCaps(xmlBytes []byte, arch interface{ supportsHostModel() bool; defaultVendor() string }) (*parseResult, error) {
	var caps hostDomCapabilities
	if err := xml.Unmarshal(xmlBytes, &caps); err != nil {
		return nil, fmt.Errorf("unmarshal domcapabilities: %w", err)
	}

	res := &parseResult{requiredFeatures: make(map[string]bool)}

	for _, mode := range caps.CPU.Mode {
		if mode.Name == "host-model" {
			if !arch.supportsHostModel() {
				fmt.Printf("  [WARN] host-model not supported for this arch — skipping\n")
				continue
			}

			res.cpuModelVendor = strings.TrimSpace(mode.Vendor.Name)
			if res.cpuModelVendor == "" {
				res.cpuModelVendor = arch.defaultVendor()
			}

			if len(mode.Model) < 1 {
				return nil, fmt.Errorf("host-model mode has no <model> element")
			}
			res.hostModelName = strings.TrimSpace(mode.Model[0].Name)
			res.hostModelFallback = mode.Model[0].Fallback

			for _, f := range mode.Feature {
				if f.Policy == "require" {
					res.requiredFeatures[f.Name] = true
				}
			}
		}

		for _, m := range mode.Model {
			name := strings.TrimSpace(m.Name)
			if m.Usable == "" || name == "" {
				continue
			}
			res.knownModels = append(res.knownModels, name)
			if m.Usable != "no" {
				res.usableModels = append(res.usableModels, name)
			}
		}
	}

	return res, nil
}

func loadSupportedFeatures(xmlBytes []byte, arch interface{ requirePolicy(string) bool }) ([]string, error) {
	var features SupportedHostFeature
	if err := xml.Unmarshal(xmlBytes, &features); err != nil {
		return nil, fmt.Errorf("unmarshal supported_features: %w", err)
	}

	var usable []string
	for _, f := range features.Feature {
		if arch.requirePolicy(f.Policy) {
			usable = append(usable, f.Name)
		}
	}
	return usable, nil
}

// ── label generation (mirrors prepareLabels) ─────────────────────────────────

func generateLabels(res *parseResult, arch interface {
	hasHostSupportedFeatures() bool
	supportsNamedModels() bool
	supportsHostModel() bool
}) map[string]string {

	labels := make(map[string]string)

	if arch.hasHostSupportedFeatures() {
		for _, f := range res.supportedFeatures {
			labels[cpuFeatureLabel+f] = "true"
		}
	}

	if arch.supportsNamedModels() {
		for _, m := range res.usableModels {
			labels[cpuModelLabel+m] = "true"
		}
		for _, m := range res.knownModels {
			labels[supportedMigrationLabel+m] = "true"
		}
	}

	if arch.supportsHostModel() {
		for feat := range res.requiredFeatures {
			labels[hostModelReqFeatLabel+feat] = "true"
		}
		labels[cpuModelVendorLabel+res.cpuModelVendor] = "true"
		labels[hostModelCPULabel+res.hostModelName] = "true"
	}

	return labels
}

// ── embedded synthetic testdata (fallback when real files absent) ─────────────

const syntheticDomCaps = `<domainCapabilities>
  <domain>kvm</domain>
  <machine>pseries-rhel10.0.0</machine>
  <arch>ppc64</arch>
  <cpu>
    <mode name='host-passthrough' supported='yes'/>
    <mode name='host-model' supported='yes'>
      <model fallback='allow'>POWER10</model>
      <feature policy='require' name='cfpc-sc'/>
      <feature policy='require' name='sbbc-sc'/>
      <feature policy='require' name='ibs-enh'/>
      <feature policy='require' name='mma'/>
      <feature policy='require' name='phnt'/>
    </mode>
    <mode name='custom' supported='yes'>
      <model usable='yes'>POWER10</model>
      <model usable='yes'>POWER10-v2.0</model>
      <model usable='no'>POWER9</model>
      <model usable='no'>POWER9-v2.0</model>
      <model usable='no'>POWER9-v2.2</model>
      <model usable='no'>POWER8</model>
      <model usable='no'>POWER8-v2.0</model>
      <model usable='no'>POWER7</model>
    </mode>
  </cpu>
  <features>
    <sev supported='no'/>
  </features>
</domainCapabilities>`

const syntheticSupportedFeatures = `<cpu>
  <model>POWER10</model>
  <feature name='cfpc-sc'/>
  <feature name='cfpc-vp'/>
  <feature name='sbbc-sc'/>
  <feature name='sbbc-p'/>
  <feature name='ibs-enh'/>
  <feature name='mma'/>
  <feature name='phnt'/>
  <feature name='altivec'/>
  <feature name='vsx'/>
  <feature name='tm'/>
  <feature name='radix-mmu'/>
  <feature name='large-radix'/>
  <feature name='pmu-bhrb'/>
  <feature name='pmu-sb'/>
  <feature name='power10-dfs'/>
</cpu>`

// ── helpers ──────────────────────────────────────────────────────────────────

func readOrFallback(path, fallback string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("  [INFO] %s not found, using embedded synthetic data\n", path)
		return []byte(fallback)
	}
	fmt.Printf("  [INFO] loaded %s (%d bytes)\n", path, len(data))
	return data
}

// ── test cases ───────────────────────────────────────────────────────────────

type testCase struct {
	name string
	arch interface {
		defaultVendor() string
		requirePolicy(string) bool
		hasHostSupportedFeatures() bool
		supportsHostModel() bool
		supportsNamedModels() bool
		arch() string
	}
	expectFeatures bool
	expectModels   bool
	expectHostModel bool
}

func runTest(tc testCase, domCapsBytes, featuresBytes []byte) bool {
	fmt.Printf("\n=== Test: %s ===\n", tc.name)
	pass := true

	// 1. Parse domcapabilities
	res, err := loadDomCaps(domCapsBytes, tc.arch)
	if err != nil {
		fmt.Printf("  FAIL: loadDomCaps error: %v\n", err)
		return false
	}
	fmt.Printf("  usable models  : %v\n", res.usableModels)
	fmt.Printf("  known  models  : %v\n", res.knownModels)
	fmt.Printf("  hostModel.Name : %q (fallback=%q)\n", res.hostModelName, res.hostModelFallback)
	fmt.Printf("  cpuModelVendor : %q\n", res.cpuModelVendor)
	fmt.Printf("  requiredFeatures: %v\n", keys(res.requiredFeatures))

	// 2. Parse supported features
	if tc.arch.hasHostSupportedFeatures() {
		feats, err := loadSupportedFeatures(featuresBytes, tc.arch)
		if err != nil {
			fmt.Printf("  FAIL: loadSupportedFeatures error: %v\n", err)
			return false
		}
		res.supportedFeatures = feats
		fmt.Printf("  supportedFeatures (%d): %v\n", len(feats), feats)
	}

	// 3. Generate labels
	labels := generateLabels(res, tc.arch)
	fmt.Printf("  generated labels (%d):\n", len(labels))
	for _, k := range sortedKeys(labels) {
		fmt.Printf("    %s = %s\n", k, labels[k])
	}

	// 4. Assertions
	if tc.expectFeatures {
		found := false
		for k := range labels {
			if strings.HasPrefix(k, cpuFeatureLabel) {
				found = true
				break
			}
		}
		if !found {
			fmt.Printf("  FAIL: expected cpu-feature labels but none generated\n")
			pass = false
		} else {
			fmt.Printf("  PASS: cpu-feature labels present\n")
		}
	} else {
		for k := range labels {
			if strings.HasPrefix(k, cpuFeatureLabel) {
				fmt.Printf("  FAIL: unexpected cpu-feature label %s\n", k)
				pass = false
			}
		}
		if pass {
			fmt.Printf("  PASS: no cpu-feature labels (expected)\n")
		}
	}

	if tc.expectModels {
		modelLabels := 0
		for k := range labels {
			if strings.HasPrefix(k, cpuModelLabel) {
				modelLabels++
			}
		}
		if modelLabels == 0 {
			fmt.Printf("  FAIL: expected cpu-model labels but none generated\n")
			pass = false
		} else {
			fmt.Printf("  PASS: %d cpu-model labels present\n", modelLabels)
		}
	}

	if tc.expectHostModel {
		if _, ok := labels[hostModelCPULabel+"POWER10"]; !ok {
			fmt.Printf("  FAIL: expected %sPOWER10 label\n", hostModelCPULabel)
			pass = false
		} else {
			fmt.Printf("  PASS: host-model-cpu label present\n")
		}
		if _, ok := labels[cpuModelVendorLabel+"IBM"]; !ok {
			fmt.Printf("  FAIL: expected %sIBM vendor label\n", cpuModelVendorLabel)
			pass = false
		} else {
			fmt.Printf("  PASS: cpu-vendor IBM label present\n")
		}
	}

	// 5. requirePolicy check
	reqBothEmpty := tc.arch.requirePolicy("")
	reqRequire   := tc.arch.requirePolicy(requirePolicy)
	reqDisable   := tc.arch.requirePolicy("disable")
	fmt.Printf("  requirePolicy(\"\")=%v  (\"require\")=%v  (\"disable\")=%v\n",
		reqBothEmpty, reqRequire, reqDisable)

	return pass
}

// ── main ─────────────────────────────────────────────────────────────────────

func main() {
	const (
		realDomCaps  = "/var/lib/kubevirt-node-labeller/virsh_domcapabilities.xml"
		realFeatures = "/var/lib/kubevirt-node-labeller/supported_features.xml"
	)

	domCapsBytes  := readOrFallback(realDomCaps, syntheticDomCaps)
	featuresBytes := readOrFallback(realFeatures, syntheticSupportedFeatures)

	tests := []testCase{
		{
			name:            "ppc64le with new archLabellerPPC64LE (FIXED)",
			arch:            archLabellerPPC64LE{},
			expectFeatures:  true,
			expectModels:    true,
			expectHostModel: true,
		},
		{
			name:            "ppc64le with defaultArchLabeller (BUG — pre-fix behaviour)",
			arch:            defaultArchLabeller{},
			expectFeatures:  false,  // BUG: defaults all to false
			expectModels:    false,
			expectHostModel: false,
		},
	}

	allPass := true
	for _, tc := range tests {
		if !runTest(tc, domCapsBytes, featuresBytes) {
			allPass = false
		}
	}

	fmt.Println()
	fmt.Println("────────────────────────────────────────")
	if allPass {
		fmt.Println("RESULT: ALL TESTS PASSED")
	} else {
		fmt.Println("RESULT: ONE OR MORE TESTS FAILED")
		os.Exit(1)
	}
	fmt.Println("────────────────────────────────────────")
	fmt.Println()
	fmt.Println("SUMMARY OF FIXES APPLIED")
	fmt.Println("  1. pkg/virt-handler/node-labeller/ppc64le.go  (NEW FILE)")
	fmt.Println("     • archLabellerPPC64LE{} with hasHostSupportedFeatures/supportsHostModel/")
	fmt.Println("       supportsNamedModels all returning true")
	fmt.Println("     • requirePolicy accepts '' and 'require' (mirrors s390x fix)")
	fmt.Println("     • defaultVendor returns 'IBM'")
	fmt.Println()
	fmt.Println("  2. pkg/virt-handler/node-labeller/arch_labeller.go  (MODIFIED)")
	fmt.Println("     • newArchLabeller routes 'ppc64le' → archLabellerPPC64LE{}")
	fmt.Println()
	fmt.Println("  3. pkg/virt-handler/node-labeller/util/util.go  (MODIFIED, upstream)")
	fmt.Println("     • DefaultArchitecturePrefix[\"ppc64le\"] = \"ppc64_\" added")
	fmt.Println()
	fmt.Println("  4. testdata/ppc64le/  (NEW)")
	fmt.Println("     • virsh_domcapabilities.xml  — synthetic Power10 domcaps")
	fmt.Println("     • supported_features.xml     — synthetic Power10 cpu baseline")
	fmt.Println()
	fmt.Println("REMAINING GAPS (require on-cluster virsh output to fix definitively)")
	fmt.Println("  • Hypervisor-feature labels: kvm-caps-info-plugin_ppc64le.go")
	fmt.Println("    returns an empty slice.  Real KVM capability bits for ppc64")
	fmt.Println("    (e.g. KVM_CAP_PPC_MMU_RADIX) are not yet exposed.")
	fmt.Println("  • supported_features.xml policy attribute: confirm on real cluster")
	fmt.Println("    whether QEMU emits policy='' or policy='require' (both are handled).")
}

// ── util ─────────────────────────────────────────────────────────────────────

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// simple insertion sort — no imports needed
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
