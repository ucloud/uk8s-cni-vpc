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

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"

	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	"google.golang.org/grpc"
)

const (
	IpamdServiceSocket = "unix:" + ipamd.IpamdServiceSocket
	CNIVpcDbName       = "cni-vpc-network"
	storageFile        = "/opt/cni/networkbolt.db"

	UAPIErrorIPNotExst = 58221
)

// Get local bolt db storage for cni-vpc-network
func accessToPodNetworkDB(dbName, storageFile string) (database.Database[rpc.PodNetwork], error) {
	db, err := database.BoltHandler(storageFile)
	if err != nil {
		ulog.Errorf("Create boltdb file handler error: %v", err)
		return nil, err
	}
	return database.NewBolt[rpc.PodNetwork](dbName, db)
}

// If there is ipamd daemon service, use ipamd to allocate Pod Ip;
// if not, do this on myself.
func assignPodIp(podName, podNS, netNS, sandboxId string) (*rpc.PodNetwork, bool, error) {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err == nil {
		// There are two prerequisites for using ipamd:
		// 1. The connection is successfully established, that is, Dial ok.
		// 2. Check ipamd with ping request, it is in a healthy state.
		defer conn.Close()
		c := rpc.NewCNIIpamClient(conn)
		if enabledIpamd(c) {
			ip, err := allocateSecondaryIPFromIpamd(c, podName, podNS, netNS, sandboxId)
			if err != nil {
				return nil, false, fmt.Errorf("failed to call ipamd: %v", err)
			}
			return ip, true, nil
		}
	}

	uapi, err := uapi.NewClient()
	if err != nil {
		return nil, false, fmt.Errorf("failed to init uapi client: %v", err)
	}
	macAddr, err := iputils.GetNodeMacAddress("")
	if err != nil {
		return nil, false, fmt.Errorf("failed to get addr: %v", err)
	}

	// ipamd not available, directly call vpc to allocate IP
	ip, err := allocateSecondaryIP(uapi, macAddr, podName, podNS, sandboxId)
	if err != nil {
		return nil, false, fmt.Errorf("failed to call vpc: %v", err)
	}
	return ip, false, nil
}

// If there is ipamd daemon service, use ipamd to release Pod Ip;
// if not, do this on myself.
func releasePodIp(podName, podNS, sandboxId string, pNet *rpc.PodNetwork) error {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err != nil {
		// Cannot establish gRPC unix domain connection to ipamd
		// If pod has dedicated uni, leave this to ipamd when it is reinstalled
		if pNet.DedicatedUNI {
			return nil
		}
		return deallocateSecondaryIP(pNet)
	}
	defer conn.Close()
	c := rpc.NewCNIIpamClient(conn)
	if enabledIpamd(c) {
		return deallocateSecondaryIPFromIpamd(c, podName, podNS, sandboxId, pNet)
	} else {
		if pNet.DedicatedUNI {
			return nil
		}
		return deallocateSecondaryIP(pNet)
	}
}

func allocateSecondaryIP(client *uapi.ApiClient, macAddr string, podName, podNS, sandboxID string) (*rpc.PodNetwork, error) {
	cli, err := client.VPCClient()
	if err != nil {
		return nil, fmt.Errorf("failed to init vpc client: %v", err)
	}

	req := cli.NewAllocateSecondaryIpRequest()
	req.Mac = &macAddr
	ObjectId, err := uapi.GetObjectIDForSecondaryIP()
	if err != nil {
		ObjectId = client.InstanceID()
	}

	req.ObjectId = ucloud.String(ObjectId)
	req.Zone = ucloud.String(client.AvailabilityZone())
	req.VPCId = ucloud.String(client.VPCID())
	req.SubnetId = ucloud.String(client.SubnetID())

	resp, err := cli.AllocateSecondaryIp(req)
	if err != nil {
		ulog.Errorf("AllocateSecondaryIp for unetwork api service error: %v", err)
		return nil, fmt.Errorf("failed to call api: %v", err)
	}

	// Record PodNetwork Information in local storage
	return &rpc.PodNetwork{
		PodName:      podName,
		PodNS:        podNS,
		SandboxID:    sandboxID,
		DedicatedUNI: false,
		VPCIP:        resp.IpInfo.Ip,
		VPCID:        resp.IpInfo.VPCId,
		SubnetID:     resp.IpInfo.SubnetId,
		Gateway:      resp.IpInfo.Gateway,
		Mask:         resp.IpInfo.Mask,
		MacAddress:   resp.IpInfo.Mac,
		CreateTime:   time.Now().Unix(),
	}, nil
}

func checkSecondaryIPExist(ip, mac string) (bool, error) {
	uapi, err := uapi.NewClient()
	if err != nil {
		return false, err
	}
	cli, err := uapi.VPCClient()
	if err != nil {
		return false, err
	}

	req := cli.NewDescribeSecondaryIpRequest()
	req.Ip = ucloud.String(ip)
	req.Mac = ucloud.String(mac)
	req.Zone = ucloud.String(uapi.AvailabilityZone())
	req.VPCId = ucloud.String(uapi.VPCID())
	req.SubnetId = ucloud.String(uapi.SubnetID())
	resp, err := cli.DescribeSecondaryIp(req)
	if err != nil {
		ulog.Errorf("DescribeSecondaryIp %s error: %v", ip, err)
		return false, err
	}
	if len(resp.DataSet) > 0 {
		return true, nil
	}
	return false, nil
}

func deallocateSecondaryIP(pNet *rpc.PodNetwork) error {
	if pNet.MacAddress == "" {
		macAddr, err := iputils.GetNodeMacAddress("")
		if err == nil {
			pNet.MacAddress = macAddr
		} else {
			return fmt.Errorf("Can't get default mac address %v", err)
		}
	}
	exist, err := checkSecondaryIPExist(pNet.VPCIP, pNet.MacAddress)
	if err != nil {
		return fmt.Errorf("cannot find secondary ip %s, %v", pNet.VPCIP, err)
	}
	if !exist {
		ulog.Infof("Secondary Ip %s has already been deleted in previous cni command DEL", pNet.VPCIP)
		return nil
	}

	// Create UCloud api client config
	client, err := uapi.NewClient()
	if err != nil {
		return err
	}
	cli, err := client.VPCClient()
	if err != nil {
		return err
	}

	req := cli.NewDeleteSecondaryIpRequest()
	objectId, err := uapi.GetObjectIDForSecondaryIP()
	if err != nil {
		objectId = client.InstanceID()
	}

	req.Zone = ucloud.String(client.AvailabilityZone())
	req.Mac = ucloud.String(pNet.MacAddress)
	req.Ip = ucloud.String(pNet.VPCIP)
	req.ObjectId = ucloud.String(objectId)
	req.VPCId = ucloud.String(pNet.VPCID)
	req.SubnetId = ucloud.String(pNet.SubnetID)

	resp, err := cli.DeleteSecondaryIp(req)
	if err != nil {
		if resp.RetCode == UAPIErrorIPNotExst {
			ulog.Warnf("Secondary ip %s has been deleted before", pNet.VPCIP)
			return nil
		}
		ulog.Errorf("Delete secondary ip error: %v, request is %+v", err, req)
	} else {
		ulog.Infof("Delete secondary ip %s success.", pNet.VPCIP)
	}
	return err
}

// Check if there is ipamd service available by a gRPC Ping probe.
func enabledIpamd(c rpc.CNIIpamClient) bool {
	_, err := c.Ping(context.Background(), &rpc.PingRequest{})
	if err != nil {
		return false
	}
	return true
}

func allocateSecondaryIPFromIpamd(c rpc.CNIIpamClient, podName, podNS, netNS, sandboxID string) (*rpc.PodNetwork, error) {
	r, err := c.AddPodNetwork(context.Background(),
		&rpc.AddPodNetworkRequest{
			PodName:      podName,
			PodNamespace: podNS,
			SandboxID:    sandboxID,
			Netns:        netNS,
		})

	if err != nil {
		ulog.Errorf("Error received from AddPodNetwork gRPC call for pod %s namespace %s container %s: %v", podName, podNS, sandboxID, err)
		return nil, err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		ulog.Errorf("gRPC AddPodNetwork failed, code %v", r.Code)
		return nil, fmt.Errorf("gRPC AddPodNetwork failed, code %v", r.Code)
	}

	return r.GetPodNetwork(), nil
}

func deallocateSecondaryIPFromIpamd(c rpc.CNIIpamClient, podName, podNS, podInfraContainerID string, pNet *rpc.PodNetwork) error {
	delRPC := &rpc.DelPodNetworkRequest{
		PodNetwork: pNet,
	}
	r, err := c.DelPodNetwork(context.Background(), delRPC)

	if err != nil {
		ulog.Errorf("Error received from DelPodNetwork gRPC call for pod %s namespace %s container %s: %v", podName, podNS, podInfraContainerID, err)
		return err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		ulog.Errorf("Error code received from DelPodNetwork gRPC call for pod %s namespace %s container %s: %v",
			podName, podNS, podInfraContainerID, r.Code)
		return fmt.Errorf("DelPodNetwork Code %v", r.Code)
	}

	return nil
}

// If there is ipamd daemon service, use ipamd to add PodNetworkRecord;
// if not, do this on myself.
func addPodNetworkRecord(podName, podNS, sandBoxID string, pNet *rpc.PodNetwork) error {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err != nil {
		return addPodNetworkRecordLocal(podName, podNS, sandBoxID, pNet)
	}
	defer conn.Close()
	c := rpc.NewCNIIpamClient(conn)
	if enabledIpamd(c) {
		return addPodNetworkRecordFromIpamd(c, podName, podNS, sandBoxID, pNet)
	} else {
		return addPodNetworkRecordLocal(podName, podNS, sandBoxID, pNet)
	}
}

func addPodNetworkRecordLocal(podName, podNS, sandBoxID string, pNet *rpc.PodNetwork) error {
	db, err := accessToPodNetworkDB(CNIVpcDbName, storageFile)
	if err != nil {
		releasePodIp(podName, podNS, sandBoxID, pNet)
		return err
	}
	defer db.Close()
	return db.Put(database.PodKey(podName, podNS, sandBoxID), pNet)
}

func addPodNetworkRecordFromIpamd(c rpc.CNIIpamClient, podName, podNS, sandBoxID string, pNet *rpc.PodNetwork) error {
	r, err := c.AddPodNetworkRecord(context.Background(),
		&rpc.AddPodNetworkRecordRequest{
			PodNetwork: pNet,
		})

	if err != nil {
		ulog.Errorf("Error received from AddPodNetworkRecord gRPC call for pod %s namespace %s container %s: %v",
			podName, podNS, sandBoxID, err)
		return err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		ulog.Errorf("gRPC AddPodNetworkRecord failed, code %v", r.Code)
		return fmt.Errorf("gRPC AddPodNetworkRecord failed, code %v", r.Code)
	}
	return nil
}

// If there is ipamd daemon service, use ipamd to delete NetworkRecord;
// if not, do this on myself.
func delPodNetworkRecord(podName, podNS, sandBoxID string, pNet *rpc.PodNetwork) error {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err != nil {
		return delPodNetworkRecordLocal(podName, podNS, sandBoxID, pNet)
	}
	defer conn.Close()
	c := rpc.NewCNIIpamClient(conn)
	if enabledIpamd(c) {
		return delPodNetworkRecordFromIpamd(c, podName, podNS, sandBoxID)
	} else {
		return delPodNetworkRecordLocal(podName, podNS, sandBoxID, pNet)
	}
}

func delPodNetworkRecordLocal(podName, podNS, sandBoxID string, pNet *rpc.PodNetwork) error {
	if pNet.DedicatedUNI {
		return nil
	}
	db, err := accessToPodNetworkDB(CNIVpcDbName, storageFile)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Delete(database.PodKey(podName, podNS, sandBoxID))
}

func delPodNetworkRecordFromIpamd(c rpc.CNIIpamClient, podName, podNS, sandBoxID string) error {
	r, err := c.DelPodNetworkRecord(context.Background(),
		&rpc.DelPodNetworkRecordRequest{
			PodName:   podName,
			PodNS:     podNS,
			SandboxID: sandBoxID,
		})

	if err != nil {
		ulog.Errorf("Error received from DelPodNetworkRecord gRPC call for pod %s namespace %s container %s: %v",
			podName, podNS, sandBoxID, err)
		return err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		ulog.Errorf("gRPC DelPodNetworkRecord failed, code %v", r.Code)
		return fmt.Errorf("gRPC DelPodNetworkRecord failed, code %v", r.Code)
	}
	return nil
}

// If there is ipamd daemon service, use ipamd to get PodNetworkRecord;
// if not, do this on myself.
func getPodNetworkRecord(podName, podNS, sandBoxID string) (*rpc.PodNetwork, error) {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err != nil {
		return getPodNetworkRecordLocal(podName, podNS, sandBoxID)
	}
	defer conn.Close()
	c := rpc.NewCNIIpamClient(conn)
	if enabledIpamd(c) {
		return getPodNetworkRecordFromIpamd(c, podName, podNS, sandBoxID)
	} else {
		return getPodNetworkRecordLocal(podName, podNS, sandBoxID)
	}
}

func getPodNetworkRecordLocal(podName, podNS, sandBoxID string) (*rpc.PodNetwork, error) {
	db, err := accessToPodNetworkDB(CNIVpcDbName, storageFile)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	p, err := db.Get(database.PodKey(podName, podNS, sandBoxID))
	if err != nil {
		return nil, err
	}
	return p, nil
}

func getPodNetworkRecordFromIpamd(c rpc.CNIIpamClient, podName, podNS, sandBoxID string) (*rpc.PodNetwork, error) {
	r, err := c.GetPodNetworkRecord(context.Background(),
		&rpc.GetPodNetworkRecordRequest{
			PodName:   podName,
			PodNS:     podNS,
			SandboxID: sandBoxID,
		})

	if err != nil {
		ulog.Errorf("Error received from GetPodNetworkRecord gRPC call for pod %s namespace %s container %s: %v",
			podName, podNS, sandBoxID, err)
		return nil, err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		ulog.Errorf("gRPC GetPodNetworkRecord error, code %v", r.Code)
		return nil, fmt.Errorf("gRPC GetPodNetworkRecord failed, code %v", r.Code)
	}
	return r.GetPodNetwork(), nil
}
