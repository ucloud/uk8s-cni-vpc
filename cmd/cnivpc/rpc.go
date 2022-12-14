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
	"net"
	"strings"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"

	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
	"github.com/ucloud/uk8s-cni-vpc/pkg/storage"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	log "github.com/sirupsen/logrus"

	"google.golang.org/grpc"
)

const (
	IpamdServiceSocket = "unix:" + ipamd.IpamdServiceSocket
	CNIVpcDbName       = "cni-vpc-network"
	storageFile        = "/opt/cni/networkbolt.db"

	instanceTypeCube    = "Cube"
	instanceTypeUHost   = "UHost"
	instanceTypeUPHost  = "UPM"
	instanceTypeUDocker = "UDocker"
	instanceTypeUDHost  = "UDHost"
	instanceTypeUNI     = "UNI"

	UAPIErrorIPNotExst = 58221
)

// Get node master network interface mac address
func getNodeMacAddress(dev string) (string, error) {
	i, e := net.InterfaceByName(dev)
	if e != nil {
		log.Errorf("Get mac address error for dev %s, %v", dev, e)
		return "", e
	}
	return strings.ToUpper(i.HardwareAddr.String()), nil
}

// Get local bolt db storage for cni-vpc-network
func accessToPodNetworkDB(dbName, storageFile string) (storage.Storage[rpc.PodNetwork], error) {
	db, err := storage.NewDBFileHandler(storageFile)
	if err != nil {
		log.Errorf("cannot get storage file handler:%v", err)
		return nil, err
	}
	return storage.NewDisk[rpc.PodNetwork](dbName, db)
}

// If there is ipamd daemon service, use ipamd to allocate Pod Ip;
// if not, do this on myself.
func assignPodIp(podName, podNS, netNS, sandboxId string) (*rpc.PodNetwork, bool, error) {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err != nil {
		// Cannot establish gRPC unix domain connection to ipamd
		ip, err := allocateSecondaryIP(podName, podNS, sandboxId)
		return ip, false, err
	}
	defer conn.Close()
	c := rpc.NewCNIIpamClient(conn)
	if enabledIpamd(c) {
		ip, err := allocateSecondaryIPFromIpamd(c, podName, podNS, netNS, sandboxId)
		return ip, true, err
	}
	ip, err := allocateSecondaryIP(podName, podNS, sandboxId)
	return ip, false, err
}

// If there is ipamd daemon service, use ipamd to release Pod Ip;
// if not, do this on myself.
func releasePodIp(podName, podNS, netNS, sandboxId string, pNet *rpc.PodNetwork) error {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err != nil {
		// Cannot establish gRPC unix domain connection to ipamd
		// If pod has dedicated uni, leave this to ipamd when it is reinstalled
		if pNet.DedicatedUNI {
			return nil
		}
		return deallocateSecondaryIP(podName, podNS, sandboxId, pNet)
	}
	defer conn.Close()
	c := rpc.NewCNIIpamClient(conn)
	if enabledIpamd(c) {
		return deallocateSecondaryIPFromIpamd(c, podName, podNS, netNS, sandboxId, pNet)
	} else {
		if pNet.DedicatedUNI {
			return nil
		}
		return deallocateSecondaryIP(podName, podNS, sandboxId, pNet)
	}
}

func allocateSecondaryIP(podName, podNS, sandboxID string) (*rpc.PodNetwork, error) {
	// Get node master interface hardware address
	macAddr, err := getNodeMacAddress(getMasterInterface())
	if err != nil {
		return nil, err
	}

	uapi, err := uapi.NewClient()
	if err != nil {
		return nil, err
	}
	cli, err := uapi.VPCClient()
	if err != nil {
		return nil, err
	}

	req := cli.NewAllocateSecondaryIpRequest()
	req.Mac = &macAddr
	ObjectId, err := getObjectIDforSecondaryIP()
	if err != nil {
		ObjectId = uapi.InstanceID()
	}

	req.ObjectId = ucloud.String(ObjectId)
	req.Zone = ucloud.String(uapi.AvailabilityZone())
	req.VPCId = ucloud.String(uapi.VPCID())
	req.SubnetId = ucloud.String(uapi.SubnetID())

	resp, err := cli.AllocateSecondaryIp(req)
	if err != nil {
		log.Errorf("Failed to AllocateSecondaryIp for unetwork api service, %v", err)
		return nil, err
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
		log.Errorf("DescribeSecondaryIp %s failed, %v", ip, err)
		return false, err
	}
	if len(resp.DataSet) > 0 {
		return true, nil
	}
	return false, nil
}

func instanceType(resource string) string {
	if strings.HasPrefix(resource, "uhost-") {
		return instanceTypeUHost
	} else if strings.HasPrefix(resource, "upm-") {
		return instanceTypeUPHost
	} else if strings.HasPrefix(resource, "docker-") {
		return instanceTypeUDocker
	} else if strings.HasPrefix(resource, "udhost-") {
		return instanceTypeUDHost
	} else if strings.HasPrefix(resource, "uni-") {
		return instanceTypeUNI
	} else if strings.HasPrefix(resource, "cube-") {
		return instanceTypeCube
	}

	return "Unknown"
}

func getObjectIDforSecondaryIP() (string, error) {
	uapi, err := uapi.NewClient()
	if err != nil {
		return "", err
	}
	instanceId := uapi.InstanceID()
	if instanceType(instanceId) != instanceTypeUHost {
		return instanceId, nil
	}

	cli, err := uapi.UHostClient()
	if err != nil {
		return "", err
	}

	req := cli.NewDescribeUHostInstanceRequest()
	req.UHostIds = []string{}
	resp, err := cli.DescribeUHostInstance(req)
	if err != nil || len(resp.UHostSet) == 0 {
		log.Errorf("DescribeUHostInstance for %v failed, %v", instanceId, err)
		return instanceId, nil
	}

	uhostInstance := resp.UHostSet[0]
	for _, ipset := range uhostInstance.IPSet {
		if ipset.Default == "true" {
			if len(ipset.NetworkInterfaceId) > 0 {
				return ipset.NetworkInterfaceId, nil
			}
		}
	}

	return instanceId, nil
}

func deallocateSecondaryIP(podName, podNS, podInfraContainerID string, pNet *rpc.PodNetwork) error {
	if pNet.MacAddress == "" {
		macAddr, err := getNodeMacAddress(getMasterInterface())
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
		log.Infof("Secondary Ip %s has already been deleted in previous cni command DEL", pNet.VPCIP)
		return nil
	}

	// Create UCloud api client config
	uapi, err := uapi.NewClient()
	if err != nil {
		return err
	}
	cli, err := uapi.VPCClient()
	if err != nil {
		return err
	}

	req := cli.NewDeleteSecondaryIpRequest()
	objectId, err := getObjectIDforSecondaryIP()
	if err != nil {
		objectId = uapi.InstanceID()
	}

	req.Zone = ucloud.String(uapi.AvailabilityZone())
	req.Mac = ucloud.String(pNet.MacAddress)
	req.Ip = ucloud.String(pNet.VPCIP)
	req.ObjectId = ucloud.String(objectId)
	req.VPCId = ucloud.String(pNet.VPCID)
	req.SubnetId = ucloud.String(pNet.SubnetID)

	resp, err := cli.DeleteSecondaryIp(req)
	if err != nil {
		if resp.RetCode == UAPIErrorIPNotExst {
			log.Warningf("Secondary ip %s has been deleted before", pNet.VPCIP)
			return nil
		}
		log.Errorf("Delete secondary ip failed, request is %+v, err is %+v ", req, err)
	} else {
		log.Infof("Delete secondary ip %s success.", pNet.VPCIP)
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
		log.Errorf("Error received from AddPodNetwork gRPC call for pod %s namespace %s container %s: %v", podName, podNS, sandboxID, err)
		return nil, err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		log.Errorf("gRPC AddPodNetwork failed, code %v", r.Code)
		return nil, fmt.Errorf("gRPC AddPodNetwork failed, code %v", r.Code)
	}

	return r.GetPodNetwork(), nil
}

func deallocateSecondaryIPFromIpamd(c rpc.CNIIpamClient, podName, podNS, netNS, podInfraContainerID string, pNet *rpc.PodNetwork) error {
	delRPC := &rpc.DelPodNetworkRequest{
		PodNetwork: pNet,
	}
	r, err := c.DelPodNetwork(context.Background(), delRPC)

	if err != nil {
		log.Errorf("Error received from DelPodNetwork gRPC call for pod %s namespace %s container %s: %v", podName, podNS, podInfraContainerID, err)
		return err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		log.Errorf("Error code received from DelPodNetwork gRPC call for pod %s namespace %s container %s: %v",
			podName, podNS, podInfraContainerID, r.Code)
		return fmt.Errorf("DelPodNetwork Code %v", r.Code)
	}

	return nil
}

// If there is ipamd daemon service, use ipamd to add PodNetworkRecord;
// if not, do this on myself.
func addPodNetworkRecord(podName, podNS, sandBoxID, netNS string, pNet *rpc.PodNetwork) error {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err != nil {
		return addPodNetworkRecordLocal(podName, podNS, sandBoxID, netNS, pNet)
	}
	defer conn.Close()
	c := rpc.NewCNIIpamClient(conn)
	if enabledIpamd(c) {
		return addPodNetworkRecordFromIpamd(c, podName, podNS, sandBoxID, pNet)
	} else {
		return addPodNetworkRecordLocal(podName, podNS, sandBoxID, netNS, pNet)
	}
}

func addPodNetworkRecordLocal(podName, podNS, sandBoxID, netNS string, pNet *rpc.PodNetwork) error {
	store, err := accessToPodNetworkDB(CNIVpcDbName, storageFile)
	if err != nil {
		log.Errorf("cannot get storage db handler: %v", err)
		releasePodIp(podName, podNS, netNS, sandBoxID, pNet)
		return err
	}
	defer store.Close()
	return store.Set(storage.GetKey(podName, podNS, sandBoxID), pNet)
}

func addPodNetworkRecordFromIpamd(c rpc.CNIIpamClient, podName, podNS, sandBoxID string, pNet *rpc.PodNetwork) error {
	r, err := c.AddPodNetworkRecord(context.Background(),
		&rpc.AddPodNetworkRecordRequest{
			PodNetwork: pNet,
		})

	if err != nil {
		log.Errorf("Error received from AddPodNetworkRecord gRPC call for pod %s namespace %s container %s: %v",
			podName, podNS, sandBoxID, err)
		return err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		log.Errorf("gRPC AddPodNetworkRecord failed, code %v", r.Code)
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
	store, err := accessToPodNetworkDB(CNIVpcDbName, storageFile)
	if err != nil {
		log.Errorf("cannot get storage db handler:%v", err)
		return err
	}
	defer store.Close()
	return store.Delete(storage.GetKey(podName, podNS, sandBoxID))
}

func delPodNetworkRecordFromIpamd(c rpc.CNIIpamClient, podName, podNS, sandBoxID string) error {
	r, err := c.DelPodNetworkRecord(context.Background(),
		&rpc.DelPodNetworkRecordRequest{
			PodName:   podName,
			PodNS:     podNS,
			SandboxID: sandBoxID,
		})

	if err != nil {
		log.Errorf("Error received from DelPodNetworkRecord gRPC call for pod %s namespace %s container %s: %v",
			podName, podNS, sandBoxID, err)
		return err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		log.Errorf("gRPC DelPodNetworkRecord failed, code %v", r.Code)
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
	store, err := accessToPodNetworkDB(CNIVpcDbName, storageFile)
	if err != nil {
		log.Errorf("Cannot get storage db handler: %v", err)
		return nil, err
	}
	defer store.Close()
	p, err := store.Get(storage.GetKey(podName, podNS, sandBoxID))
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
		log.Errorf("Error received from GetPodNetworkRecord gRPC call for pod %s namespace %s container %s: %v",
			podName, podNS, sandBoxID, err)
		return nil, err
	}

	if r.Code != rpc.CNIErrorCode_CNISuccess {
		log.Errorf("gRPC GetPodNetworkRecord failed, code %v", r.Code)
		return nil, fmt.Errorf("gRPC GetPodNetworkRecord failed, code %v", r.Code)
	}
	return r.GetPodNetwork(), nil
}
