/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright The KubeVirt Authors.
 *
 */

package nodelabeller

import "kubevirt.io/kubevirt/pkg/virt-handler/node-labeller/util"

// Ensure that there is a compile error should the struct not implement the archLabeller interface anymore.
var _ = archLabeller(&archLabellerPPC64LE{})

const ppc64le = "ppc64le"

type archLabellerPPC64LE struct{}

func (archLabellerPPC64LE) defaultVendor() string {
	// On ppc64le the virsh domcapabilities XML does not include a CPU vendor
	// element; IBM is the only vendor for Power hardware.
	return "IBM"
}

// requirePolicy mirrors the s390x behaviour: QEMU on Power does not emit a
// policy attribute on <feature> elements inside the supported-features
// baseline XML, so we accept both "require" and the empty string.
func (archLabellerPPC64LE) requirePolicy(policy string) bool {
	return policy == util.RequirePolicy || policy == ""
}

func (archLabellerPPC64LE) hasHostSupportedFeatures() bool {
	return true
}

func (archLabellerPPC64LE) supportsHostModel() bool {
	return true
}

func (archLabellerPPC64LE) supportsNamedModels() bool {
	return true
}

func (archLabellerPPC64LE) arch() string {
	return ppc64le
}
