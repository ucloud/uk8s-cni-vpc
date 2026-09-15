// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"net"
	"syscall"

	"github.com/cockroachdb/errors"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
	"github.com/ucloud/uk8s-cni-vpc/pkg/kubeclient"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
)

// The set contains Pod IPs whose outbound NAT is handled by the NAT gateway.
const natGWOutgoingEnabledIPSetName = "UCLOUD-NATGW-OUTGOING"

func ensureNATGWOutgoingIPSet() error {
	err := netlink.IpsetCreate(
		natGWOutgoingEnabledIPSetName,
		"hash:ip",
		netlink.IpsetCreateOptions{Replace: true},
	)
	if err != nil {
		return errors.Wrap(err, "main.ensureNATGWOutgoingIPSet create")
	}
	return nil
}

func natGWOutgoingIPSetEntry(podIP string) (*netlink.IPSetEntry, error) {
	ip := net.ParseIP(podIP)
	if ip == nil || ip.To4() == nil {
		return nil, errors.Errorf(
			"main.natGWOutgoingIPSetEntry invalid IPv4 address %q",
			errors.Safe(podIP),
		)
	}
	return &netlink.IPSetEntry{IP: ip.To4()}, nil
}

// migrateLegacyNATGWOutgoingIPs maps persisted Pod IPs to the PodNetworking
// selected by each live Pod on this node.
func migrateLegacyNATGWOutgoingIPs(nodeName string) error {
	if nodeName == "" {
		return errors.New("main.migrateLegacyNATGWOutgoingIPs missing node name")
	}

	networks, err := listPodNetworkRecords()
	if err != nil {
		return err
	}

	kubeClient, err := kubeclient.GetNodeClient()
	if err != nil {
		return errors.Wrap(err, "main.migrateLegacyNATGWOutgoingIPs get Kubernetes client")
	}
	podList, err := kubeClient.CoreV1().Pods(v1.NamespaceAll).List(
		context.TODO(),
		metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("spec.nodeName", nodeName).String(),
		},
	)
	if err != nil {
		return errors.Wrapf(
			err,
			"main.migrateLegacyNATGWOutgoingIPs list Pods on node %s",
			errors.Safe(nodeName),
		)
	}
	podsByName := make(map[types.NamespacedName]*v1.Pod, len(podList.Items))
	for i := range podList.Items {
		pod := &podList.Items[i]
		podsByName[types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}] = pod
	}

	crdClient, err := kubeclient.GetNodeCRDClient()
	if err != nil {
		return errors.Wrap(err, "main.migrateLegacyNATGWOutgoingIPs get CRD client")
	}
	podNetworkings, err := crdClient.VpcV1beta1().PodNetworkings().List(
		context.TODO(),
		metav1.ListOptions{},
	)
	if err != nil {
		return errors.Wrap(err, "main.migrateLegacyNATGWOutgoingIPs list PodNetworkings")
	}
	podNetworkingPolicies := make(map[string]bool, len(podNetworkings.Items))
	for i := range podNetworkings.Items {
		podNetworking := &podNetworkings.Items[i]
		podNetworkingPolicies[podNetworking.Name] = podNetworking.Spec.NATGWOutgoingEnabled
	}

	desiredPodIPs := desiredNATGWOutgoingIPs(networks, podsByName, podNetworkingPolicies)

	// currentPodIPs is the actual kernel ipset state before migration.
	currentIPSet, err := netlink.IpsetList(natGWOutgoingEnabledIPSetName)
	if err != nil {
		return errors.Wrap(err, "main.migrateLegacyNATGWOutgoingIPs list current ipset")
	}
	currentPodIPs := currentNATGWOutgoingIPs(currentIPSet.Entries)

	// Add desired members that are missing, then remove current members that
	// are no longer desired. The two differences fully describe the migration.
	toAdd := desiredPodIPs.Difference(currentPodIPs)
	toDelete := currentPodIPs.Difference(desiredPodIPs)
	for podIP := range toAdd {
		if _, err := syncNATGWOutgoingIP(podIP, true); err != nil {
			return err
		}
	}
	for podIP := range toDelete {
		if err := deleteNATGWOutgoingIP(podIP); err != nil {
			return err
		}
	}

	ulog.Infof(
		"Migrated NAT gateway outgoing ipset: desired=%d current=%d add=%d delete=%d",
		desiredPodIPs.Len(), currentPodIPs.Len(), toAdd.Len(), toDelete.Len(),
	)
	return nil
}

// desiredNATGWOutgoingIPs derives the target ipset state from live Pod
// selections and persisted Pod IPs. Invalid individual records are ignored so
// one stale object cannot prevent the remaining records from converging.
func desiredNATGWOutgoingIPs(
	networks []*rpc.PodNetwork,
	podsByName map[types.NamespacedName]*v1.Pod,
	podNetworkingPolicies map[string]bool,
) sets.Set[string] {
	desiredPodIPs := sets.New[string]()
	for _, network := range networks {
		if network == nil {
			continue
		}

		pod, exists := podsByName[types.NamespacedName{
			Namespace: network.PodNS,
			Name:      network.PodName,
		}]
		if !exists {
			continue
		}
		if network.PodUID != "" && network.PodUID != string(pod.UID) {
			ulog.Warnf(
				"Ignore stale Pod network record for %s/%s: recorded UID %s, current UID %s",
				network.PodNS, network.PodName, network.PodUID, pod.UID,
			)
			continue
		}
		if pod.Annotations[ipamd.AnnotationPodNetworkingDisable] == "true" {
			continue
		}

		podNetworkingName := pod.Annotations[ipamd.AnnotationPodNetworkingName]
		if podNetworkingName == "" {
			podNetworkingName = DefaultPodNetworkingName
		}
		natGWOutgoingEnabled, exists := podNetworkingPolicies[podNetworkingName]
		if !exists {
			if podNetworkingName != DefaultPodNetworkingName {
				ulog.Warnf(
					"Ignore Pod network record for %s/%s during NAT gateway outgoing migration: PodNetworking %s does not exist",
					network.PodNS,
					network.PodName,
					podNetworkingName,
				)
			}
			continue
		}
		if !natGWOutgoingEnabled {
			continue
		}
		entry, err := natGWOutgoingIPSetEntry(network.VPCIP)
		if err != nil {
			ulog.Warnf(
				"Ignore Pod network record for %s/%s during NAT gateway outgoing migration: %+v",
				network.PodNS,
				network.PodName,
				err,
			)
			continue
		}
		desiredPodIPs.Insert(entry.IP.String())
	}
	return desiredPodIPs
}

// currentNATGWOutgoingIPs normalizes the kernel entries used for migration.
// Entries that cannot participate in this IPv4 ipset are left untouched.
func currentNATGWOutgoingIPs(entries []netlink.IPSetEntry) sets.Set[string] {
	currentPodIPs := sets.New[string]()
	for _, entry := range entries {
		ip := entry.IP.To4()
		if ip == nil {
			ulog.Warnf(
				"Ignore invalid IPv4 entry %s in NAT gateway outgoing ipset during migration",
				entry.IP.String(),
			)
			continue
		}
		currentPodIPs.Insert(ip.String())
	}
	return currentPodIPs
}

// syncNATGWOutgoingIP adds a Pod IP when NAT gateway outgoing is enabled.
// The disabled path is intentionally a no-op; CNI DEL and initial migration
// own residual cleanup so disabled Pods do not depend on ipset.
// The returned boolean lets callers roll back only their own side effect.
func syncNATGWOutgoingIP(podIP string, natGWOutgoingEnabled bool) (bool, error) {
	if !natGWOutgoingEnabled {
		return false, nil
	}

	if err := ensureNATGWOutgoingIPSet(); err != nil {
		return false, err
	}

	entry, err := natGWOutgoingIPSetEntry(podIP)
	if err != nil {
		return false, err
	}
	if err := netlink.IpsetAdd(natGWOutgoingEnabledIPSetName, entry); err != nil {
		if errors.Is(err, nl.IPSetError(nl.IPSET_ERR_EXIST)) {
			return false, nil
		}
		return false, errors.Wrapf(
			err,
			"main.syncNATGWOutgoingIP add %s",
			errors.Safe(podIP),
		)
	}
	return true, nil
}

func deleteNATGWOutgoingIP(podIP string) error {
	entry, err := natGWOutgoingIPSetEntry(podIP)
	if err != nil {
		return err
	}
	entry.Replace = true
	if err := netlink.IpsetDel(natGWOutgoingEnabledIPSetName, entry); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil
		}
		return errors.Wrapf(
			err,
			"main.deleteNATGWOutgoingIP delete %s",
			errors.Safe(podIP),
		)
	}
	return nil
}
