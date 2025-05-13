// Copyright UCloud. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package ipamd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/ucloud/ucloud-sdk-go/services/uk8s"
	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/arping"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
)

const (
	UAPIErrorIPNotExst       = 58221
	UAPIErrorSubnetNotEnough = 57000
)

var ErrOutOfIP = errors.New("vpc out of ip")

func (s *ipamServer) uapiAllocateSecondaryIP(pnReq *rpc.AddPodNetworkRequest) (*vpc.IpInfo, error) {
	cli, err := s.uapi.VPCClient()
	if err != nil {
		return nil, err
	}

	err = s.uapiCheckSubnetRemainsIP(pnReq.SubnetID)
	if err != nil {
		return nil, err
	}

	req := cli.NewAllocateSecondaryIpRequest()
	req.Mac = ucloud.String(pnReq.MacAddress)
	objectID := pnReq.InterfaceID
	if objectID == "" {
		objectID, err = uapi.GetObjectIDForSecondaryIP()
		if err != nil {
			objectID = s.hostId
		}
	}
	req.ObjectId = ucloud.String(objectID)
	req.Zone = ucloud.String(s.zoneId)
	req.VPCId = ucloud.String(s.uapi.VPCID())
	req.SubnetId = ucloud.String(pnReq.SubnetID)

	resp, err := cli.AllocateSecondaryIp(req)
	if err != nil {
		if resp != nil && resp.GetRetCode() == UAPIErrorSubnetNotEnough {
			return nil, ErrOutOfIP
		}
		ulog.Errorf("Invoke API AllocateSecondaryIp, response id %v, error: %v", resp.GetRequestUUID(), err)
		return nil, err
	}

	ulog.Infof("Allocated Ip %v from unetwork api service", resp.IpInfo.Ip)
	return &resp.IpInfo, nil
}

// Describe Subnet, check if it still has remain IP(s) to allocate
// If there is no remain IP, returns `ErrOutOfIP`
func (s *ipamServer) uapiCheckSubnetRemainsIP(subnetId string) error {
	cli, err := s.uapi.VPCClient()
	if err != nil {
		return err
	}

	req := cli.NewDescribeSubnetRequest()
	req.ShowAvailableIPs = ucloud.Bool(true)
	req.SubnetId = ucloud.String(subnetId)
	req.VPCId = ucloud.String(s.uapi.VPCID())

	resp, err := cli.DescribeSubnet(req)
	if err != nil {
		return err
	}

	if len(resp.DataSet) == 0 {
		return fmt.Errorf("subnet %s not found", subnetId)
	}

	subnet := resp.DataSet[0]
	ulog.Infof("Subnet remains %d ip to allocate", subnet.AvailableIPs)
	if subnet.AvailableIPs <= 0 {
		ulog.Warnf("No enough ip in subnet %s to allocate", subnetId)
		return ErrOutOfIP
	}

	return nil
}

func (s *ipamServer) checkIPConflict(ip string) error {
	s.conflictLock.Lock()
	defer s.conflictLock.Unlock()
	ulog.Infof("Begin to detect ip conflict for %s", ip)
	start := time.Now()
	conflict, err := arping.DetectIpConflictWithGratuitousArp(net.ParseIP(ip), iputils.GetMasterInterface())
	if err != nil {
		return fmt.Errorf("failed to detect conflict for ip %s: %v", ip, err)
	}
	if conflict {
		return fmt.Errorf("ip %s is still in conflict after retrying", ip)
	}
	ulog.Infof("Detect ip conflict for %s done, took %v", ip, time.Since(start))
	return nil
}

func (s *ipamServer) uapiAllocateSpecifiedSecondaryIp(ip string, pnReq *rpc.AddPodNetworkRequest) (ipInfo *vpc.IpInfo, err error) {
	cli, err := s.uapi.VPCClient()
	if err != nil {
		return nil, err
	}

	objectID := pnReq.InterfaceID
	if objectID == "" {
		objectID, err = uapi.GetObjectIDForSecondaryIP()
		if err != nil {
			objectID = s.hostId
		}
	}

	req := cli.NewAllocateSecondaryIpRequest()
	req.Mac = ucloud.String(pnReq.MacAddress)
	req.Ip = ucloud.String(ip)
	req.ObjectId = ucloud.String(objectID)
	req.Zone = ucloud.String(s.zoneId)
	req.VPCId = ucloud.String(s.uapi.VPCID())
	req.SubnetId = ucloud.String(pnReq.SubnetID)

	resp, err := cli.AllocateSecondaryIp(req)
	if err != nil {
		ulog.Errorf("Invoke API AllocateSecondaryIp, response id %v, error: %v", resp.GetRequestUUID(), err)
	}
	ulog.Infof("Allocated Ip %v from unetwork api service", resp.IpInfo.Ip)
	return &resp.IpInfo, nil
}

func (s *ipamServer) uapiDescribeSecondaryIp(ip, subnetId string) (*vpc.IpInfo, error) {
	cli, err := s.uapi.VPCClient()
	if err != nil {
		return nil, err
	}

	req := cli.NewDescribeSecondaryIpRequest()

	req.Ip = ucloud.String(ip)
	req.SubnetId = ucloud.String(subnetId)
	req.VPCId = ucloud.String(s.uapi.VPCID())
	resp, err := cli.DescribeSecondaryIp(req)
	if err != nil {
		ulog.Errorf("Describe secondaryIp %s error: %v, request id %s", ip, err, resp.GetRequestUUID())
		return nil, err
	}

	if len(resp.DataSet) == 0 {
		return nil, nil
	}
	return &(resp.DataSet[0]), nil
}

func (s *ipamServer) uapiMoveSecondaryIPMac(pn *rpc.PodNetwork, targetMacAddress string) error {
	cli, err := s.uapi.VPCClient()
	if err != nil {
		return err
	}

	req := cli.NewMoveSecondaryIPMacRequest()
	req.Ip = ucloud.String(pn.VPCIP)
	req.NewMac = ucloud.String(targetMacAddress)
	req.OldMac = ucloud.String(pn.MacAddress)
	req.SubnetId = ucloud.String(pn.SubnetID)
	_, err = cli.MoveSecondaryIPMac(req)

	return err
}

func (s *ipamServer) checkSecondaryIpExist(pn *rpc.PodNetwork) (bool, error) {
	cli, err := s.uapi.VPCClient()
	if err != nil {
		return false, err
	}

	req := cli.NewDescribeSecondaryIpRequest()
	req.Ip = ucloud.String(pn.VPCIP)
	req.Mac = ucloud.String(pn.MacAddress)
	req.Zone = ucloud.String(s.uapi.AvailabilityZone())
	req.VPCId = ucloud.String(s.uapi.VPCID())
	req.SubnetId = ucloud.String(pn.SubnetID)
	resp, err := cli.DescribeSecondaryIp(req)
	if err != nil {
		ulog.Errorf("DescribeSecondaryIp %s error: %v, request id %s", pn.VPCIP, err, resp.GetRequestUUID())
		return false, err
	}
	for _, data := range resp.DataSet {
		if data.Ip == pn.VPCIP {
			return true, nil
		}
	}
	return false, nil
}

func (s *ipamServer) uapiDeleteSecondaryIp(pn *rpc.PodNetwork) error {
	exist, err := s.checkSecondaryIpExist(pn)
	if err != nil {
		return fmt.Errorf("cannot find secondary ip %s, %v", pn.VPCIP, err)
	}
	if !exist {
		ulog.Infof("Secondary Ip %s has already been deleted in previous cni command DEL", pn.VPCIP)
		return nil
	}

	cli, err := s.uapi.VPCClient()
	if err != nil {
		return err
	}

	objectID := pn.InterfaceID
	if objectID == "" {
		objectID = s.hostId
	}

	req := cli.NewDeleteSecondaryIpRequest()
	req.Zone = ucloud.String(s.zoneId)
	req.Mac = ucloud.String(pn.MacAddress)
	req.Ip = ucloud.String(pn.VPCIP)
	req.ObjectId = ucloud.String(objectID)
	req.VPCId = ucloud.String(s.uapi.VPCID())
	req.SubnetId = ucloud.String(pn.SubnetID)

	resp, err := cli.DeleteSecondaryIp(req)
	if err == nil {
		ulog.Infof("Secondary Ip %v deleted by unetwork api service", pn.VPCIP)
	}
	if resp.RetCode == UAPIErrorIPNotExst {
		ulog.Warnf("Secondary ip %s has been deleted before", pn.VPCIP)
		return nil
	}
	return err
}

func (s *ipamServer) uapiListUK8SCluster() (*uk8s.ClusterSet, error) {
	cli, err := s.uapi.UK8SClient()
	if err != nil {
		return nil, err
	}

	clusterId := os.Getenv("UCLOUD_UK8S_CLUSTER_ID")
	if len(clusterId) == 0 {
		return nil, fmt.Errorf("cannot get cluster id by environment var UCLOUD_REGION_ID")
	}

	req := cli.NewListUK8SClusterV2Request()
	req.ClusterId = ucloud.String(clusterId)
	resp, err := cli.ListUK8SClusterV2(req)
	if err != nil {
		return nil, err
	}
	if len(resp.ClusterSet) == 0 {
		return nil, errors.New("UK8S ClusterSet is empty")
	}

	return &(resp.ClusterSet[0]), nil
}
