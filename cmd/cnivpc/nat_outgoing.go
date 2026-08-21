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

const (
	natOutgoingDisabledIPSetName = "UCLOUD-NATOUTGOING-OFF"
	metadataServiceCIDR          = "100.80.80.80/32"
)

func ensureNATOutgoingIPSet() error {
	err := netlink.IpsetCreate(
		natOutgoingDisabledIPSetName,
		"hash:ip",
		netlink.IpsetCreateOptions{Replace: true},
	)
	if err != nil {
		return errors.Wrap(err, "main.ensureNATOutgoingIPSet create")
	}
	return nil
}

func natOutgoingIPSetEntry(podIP string) (*netlink.IPSetEntry, error) {
	ip := net.ParseIP(podIP)
	if ip == nil || ip.To4() == nil {
		return nil, errors.Errorf(
			"main.natOutgoingIPSetEntry invalid IPv4 address %q",
			errors.Safe(podIP),
		)
	}
	return &netlink.IPSetEntry{IP: ip.To4()}, nil
}

func migrateLegacyNATOutgoingIPs() error {
	networks, err := listPodNetworkRecords()
	if err != nil {
		return err
	}

	for _, network := range networks {
		if network == nil {
			continue
		}
		entry, err := natOutgoingIPSetEntry(network.VPCIP)
		if err != nil {
			return err
		}
		entry.Replace = true
		if err := netlink.IpsetAdd(natOutgoingDisabledIPSetName, entry); err != nil {
			return errors.Wrapf(
				err,
				"main.migrateLegacyNATOutgoingIPs add %s",
				errors.Safe(network.VPCIP),
			)
		}
	}

	ulog.Infof("Migrated %d existing Pod IPs to NAT outgoing disabled set", len(networks))
	return nil
}

// syncNATOutgoingIP converges a Pod IP to the requested NAT outgoing policy.
// The returned boolean reports whether this call added a new no-NAT member,
// allowing callers to roll back only their own side effect.
func syncNATOutgoingIP(podIP string, natOutgoing bool) (bool, error) {
	if err := ensureNATOutgoingIPSet(); err != nil {
		return false, err
	}

	if natOutgoing {
		if err := deleteNATOutgoingIP(podIP); err != nil {
			return false, err
		}
		return false, nil
	}

	entry, err := natOutgoingIPSetEntry(podIP)
	if err != nil {
		return false, err
	}
	if err := netlink.IpsetAdd(natOutgoingDisabledIPSetName, entry); err != nil {
		if errors.Is(err, nl.IPSetError(nl.IPSET_ERR_EXIST)) {
			return false, nil
		}
		return false, errors.Wrapf(
			err,
			"main.syncNATOutgoingIP add %s",
			errors.Safe(podIP),
		)
	}
	return true, nil
}

func deleteNATOutgoingIP(podIP string) error {
	entry, err := natOutgoingIPSetEntry(podIP)
	if err != nil {
		return err
	}
	entry.Replace = true
	if err := netlink.IpsetDel(natOutgoingDisabledIPSetName, entry); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil
		}
		return errors.Wrapf(
			err,
			"main.deleteNATOutgoingIP delete %s",
			errors.Safe(podIP),
		)
	}
	return nil
}
