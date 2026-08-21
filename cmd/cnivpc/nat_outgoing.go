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
	"net"
	"syscall"

	"github.com/cockroachdb/errors"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
)

// Keep the existing kernel object name: enabling NAT gateway outgoing means
// node-side NAT outgoing is disabled for the Pod IP.
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

func migrateLegacyNATGWOutgoingIPs() error {
	networks, err := listPodNetworkRecords()
	if err != nil {
		return err
	}

	for _, network := range networks {
		if network == nil {
			continue
		}
		entry, err := natGWOutgoingIPSetEntry(network.VPCIP)
		if err != nil {
			return err
		}
		entry.Replace = true
		if err := netlink.IpsetAdd(natGWOutgoingEnabledIPSetName, entry); err != nil {
			return errors.Wrapf(
				err,
				"main.migrateLegacyNATGWOutgoingIPs add %s",
				errors.Safe(network.VPCIP),
			)
		}
	}

	ulog.Infof("Migrated %d existing Pod IPs to NAT gateway outgoing set", len(networks))
	return nil
}

// syncNATGWOutgoingIP converges a Pod IP to the requested NAT gateway policy.
// The returned boolean reports whether this call added a new NAT gateway member,
// allowing callers to roll back only their own side effect.
func syncNATGWOutgoingIP(podIP string, natGWOutgoingEnabled bool) (bool, error) {
	if err := ensureNATGWOutgoingIPSet(); err != nil {
		return false, err
	}

	if !natGWOutgoingEnabled {
		if err := deleteNATGWOutgoingIP(podIP); err != nil {
			return false, err
		}
		return false, nil
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
