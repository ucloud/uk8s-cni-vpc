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
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/ucloud-sdk-go/ucloud/request"
	podnetworkingv1beta1 "github.com/ucloud/uk8s-cni-vpc/kubernetes/apis/podnetworking/v1beta1"
	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/kubeclient"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
)

const (
	IpamdServiceSocket = "unix:" + ipamd.IpamdServiceSocket
	CNIVpcDbName       = "cni-vpc-network"
	storageFile        = "/opt/cni/networkbolt.db"

	UAPIErrorIPNotExst       = 58221
	DefaultPodNetworkingName = "default"
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

func getPodNetworkingConfig(kubeClient *kubernetes.Clientset, podName, podNS string) *podnetworkingv1beta1.PodNetworking {
	if !uapi.IsUNIFeatureUHost() {
		return nil
	}
	crdClient, err := kubeclient.GetNodeCRDClient()
	if err != nil {
		ulog.Errorf("failed to get crd kube client: %v", err)
		return nil
	}
	pod, err := kubeClient.CoreV1().Pods(podNS).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		ulog.Errorf("failed to get pod %s in namespace %s: %v", podName, podNS, err)
		return nil
	}
	disable := pod.Annotations[ipamd.AnnotationPodNetworkingDisable]
	if disable == "true" {
		// User disable podnetworking manually
		ulog.Infof("pod %s/%s disabled podnetworking", podNS, podName)
		return nil
	}
	pnName, _ := pod.Annotations[ipamd.AnnotationPodNetworkingName]
	if pnName == "" {
		pnName = DefaultPodNetworkingName
	}
	podnet, err := crdClient.PodnetworkingV1beta1().PodNetworkings().Get(context.TODO(), pnName, metav1.GetOptions{})
	if err != nil {
		if pnName != DefaultPodNetworkingName {
			ulog.Errorf("failed to get podnetworking with name %s: %v", pnName, err)
		}
		return nil
	}
	if len(podnet.Spec.SubnetIds) == 0 {
		ulog.Errorf("podnetworking %s has no subnet", pnName)
		return nil
	}
	return podnet
}

// If there is ipamd daemon service, use ipamd to allocate Pod Ip;
// if not, do this on myself.
func assignPodIp(podName, podNS, netNS, sandboxId string) (*rpc.PodNetwork, bool, error) {
	kubeClient, err := kubeclient.GetNodeClient()
	if err != nil {
		return nil, false, fmt.Errorf("failed to get node kube client: %v", err)
	}
	pnConfig := getPodNetworkingConfig(kubeClient, podName, podNS)

	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	// request ipamd only if pod is not bound to podnetworking resource
	// TODO: ipamd支持podnetworking
	if err == nil && pnConfig == nil {
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

	enableStaticIP, _, err := ipamd.IsPodEnableStaticIP(kubeClient, podName, podNS)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check pod static ip enable: %v", err)
	}
	if enableStaticIP {
		// If pod enable static ip, we donot allow it to allocate ip without ipamd
		return nil, false, fmt.Errorf("pod %s/%s enable static ip, but ipamd is not enabled", podNS, podName)
	}

	// ipamd not available, directly call vpc to allocate IP
	ip, err := allocateSecondaryIP(pnConfig, podName, podNS, sandboxId)
	if err != nil {
		return nil, false, fmt.Errorf("failed to setup secondary ip: %v", err)
	}
	return ip, false, nil
}

// If there is ipamd daemon service, use ipamd to release Pod Ip;
// if not, do this on myself.
func releasePodIp(podName, podNS, sandboxId string, pNet *rpc.PodNetwork) error {
	// 当前仅cnivpc支持通过podnetworking配置分配IP到辅助虚拟网卡上
	// TODO: ipamd support podnetworking
	if !pNet.DedicatedUNI && strings.HasPrefix(pNet.InterfaceID, "uni-") {
		return deallocateSecondaryIP(pNet)
	}

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

func allocateSecondaryIP(pnConfig *podnetworkingv1beta1.PodNetworking, podName, podNS, sandboxID string) (*rpc.PodNetwork, error) {
	client, err := uapi.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to init uapi client: %v", err)
	}
	vpccli, err := client.VPCClient()
	if err != nil {
		return nil, fmt.Errorf("failed to init vpc client: %v", err)
	}
	var subnetId, objectId, macAddr string
	if pnConfig != nil {
		uni, err := ensureSubnetUNI(vpccli, client.AvailabilityZone(), client.VPCID(), client.InstanceID(), pnConfig.Spec.SubnetIds, pnConfig.Spec.SecurityGroupIds)
		if err != nil {
			ulog.Errorf("Failed to create or attach UNI to %s: %v", client.InstanceID(), err)
			return nil, fmt.Errorf("failed to ensure UNI attached: %v", err)
		}
		if err = ensureUNIPrimaryIPRoute(uni.PrivateIpSet[0], uni.MacAddress, uni.Gateway, uni.Netmask); err != nil {
			return nil, err
		}

		objectId, macAddr = uni.InterfaceId, uni.MacAddress
	} else {
		subnetId = client.SubnetID()
		macAddr, err = iputils.GetNodeMacAddress("")
		if err != nil {
			return nil, fmt.Errorf("failed to get addr: %v", err)
		}
		objectId, err = uapi.GetObjectIDForSecondaryIP()
		if err != nil {
			objectId = client.InstanceID()
		}
	}

	req := vpccli.NewAllocateSecondaryIpRequest()
	req.Zone = ucloud.String(client.AvailabilityZone())
	req.VPCId = ucloud.String(client.VPCID())
	req.SubnetId = ucloud.String(subnetId)
	req.Mac = ucloud.String(macAddr)
	req.ObjectId = ucloud.String(objectId)
	resp, err := vpccli.AllocateSecondaryIp(req)
	if err != nil {
		ulog.Errorf("AllocateSecondaryIp for unetwork api service error: %v", err)
		return nil, fmt.Errorf("failed to call api: %v", err)
	}

	ulog.Infof("AllocateSecondaryIp %s to %s success", resp.IpInfo.Ip, objectId)
	// Record PodNetwork Information in local storage
	pNet := rpc.PodNetwork{
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
	}
	if strings.HasPrefix(objectId, "uni-") {
		pNet.InterfaceID = objectId
	}
	return &pNet, nil
}

func ensureSubnetUNI(vpccli *vpc.VPCClient, zoneId, vpcId, instanceId string, subnetIds, secGroupIds []string) (uni *vpc.NetworkInterface, err error) {
	if !uapi.IsUNIFeatureUHost() {
		return nil, errors.New("UHost/UPHost not support UNI feature")
	}
	meta, err := uapi.GetMeta()
	if err != nil {
		return nil, fmt.Errorf("Get metadata error: %v", err)
	}

	var interfaceId string
	defer func() {
		if interfaceId != "" {
			uni, err = describeNetworkInterface(vpccli, interfaceId)
		}
	}()

	ulog.Infof("Begin to check subnets: %v", subnetIds)
	var subnetId string
	for _, candicateSubnetId := range subnetIds {
		remains, err := checkSubnetRemainsIP(vpccli, vpcId, candicateSubnetId)
		if err != nil {
			ulog.Warnf("Check subnet %s remains ip error: %v, skip", candicateSubnetId, err)
			continue
		}
		if !remains {
			ulog.Warnf("Subnet %s has no available ip, skip", candicateSubnetId)
			continue
		}
		subnetId = candicateSubnetId
		break
	}
	if subnetId == "" {
		return nil, fmt.Errorf("no available subnet in %v", subnetIds)
	}
	ulog.Infof("Selected subnet %s", subnetId)

	for _, netIf := range meta.UHost.NetworkInterfaces {
		if netIf.SubnetId == subnetId && !netIf.Default {
			interfaceId = netIf.Id
			return
		}
	}
	uniLimit := ipamd.GetNodeUNILimits()
	if len(meta.UHost.NetworkInterfaces) >= uniLimit {
		return nil, fmt.Errorf("Cannot attach more than %d UNI to %v", uniLimit, instanceId)
	}

	// No available uni for subnet, need to allocate new one
	newUni, err := createNetworkInterface(vpccli, zoneId, vpcId, subnetId, secGroupIds)
	if err != nil {
		return nil, err
	}
	if len(newUni.PrivateIpSet) == 0 {
		ulog.Warnf("UNI %s has no ip, trying to delete", newUni.InterfaceId)
		deleteNetworkInterface(vpccli, newUni.InterfaceId)
		return nil, errors.New("Created new UNI with no ip")
	}

	if err = attachNetworkInterface(vpccli, newUni.InterfaceId, instanceId); err != nil {
		ulog.Warnf("Attach UNI %s to %s failed, trying to delete new UNI", newUni.InterfaceId, instanceId)
		deleteNetworkInterface(vpccli, newUni.InterfaceId)
		return nil, err
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	timeout := time.After(90 * time.Second)
EXIT:
	for {
		select {
		case <-ticker.C:
			meta, err := uapi.ReloadMeta()
			if err != nil {
				continue
			}
			for _, netIf := range meta.UHost.NetworkInterfaces {
				if netIf.SubnetId == subnetId && !netIf.Default {
					interfaceId = netIf.Id
					ulog.Infof("Refresh UNI %s info from metadata server success", interfaceId)
					break EXIT
				}
			}
		case <-timeout:
			ulog.Warnf("Cannot load UNI %s info from metadata server, trying to delete", newUni.InterfaceId)
			deleteNetworkInterface(vpccli, newUni.InterfaceId)
			return nil, err
		}
	}

	return
}

func createNetworkInterface(vpccli *vpc.VPCClient, zone, vpcId, subnet string, secGroupIds []string) (*vpc.NetworkInterfaceInfo, error) {
	req := vpccli.NewCreateNetworkInterfaceRequest()
	req.VPCId = ucloud.String(vpcId)
	req.SubnetId = ucloud.String(subnet)
	if len(secGroupIds) > 0 {
		// Set security group for this UNI
		req.PrioritySecGroup = make([]vpc.CreateNetworkInterfaceParamPrioritySecGroup, 0, len(secGroupIds))
		slices.Reverse(secGroupIds)
		for idx, secGroup := range secGroupIds {
			req.PrioritySecGroup = append(req.PrioritySecGroup, vpc.CreateNetworkInterfaceParamPrioritySecGroup{
				Priority:   ucloud.Int(idx + 1),
				SecGroupId: ucloud.String(secGroup),
			})

		}
		req.SecurityMode = ucloud.Int(1)
	}
	req.SetEncoder(request.NewJSONEncoder(vpccli.GetConfig(), vpccli.GetCredential()))

	resp, err := vpccli.CreateNetworkInterface(req)
	if err != nil {
		return nil, fmt.Errorf("CreateNetworkInterface from unetwork api service error: %v", err)
	}
	if resp.RetCode != 0 {
		return nil, fmt.Errorf("CreateNetworkInterface from unetwork api error %d: %s, %s", resp.RetCode, resp.Message, resp.GetRequestUUID())
	}
	ulog.Infof("Create UNI %s from subnet %s success, %s", resp.NetworkInterface.InterfaceId, subnet, resp.GetRequestUUID())
	return &resp.NetworkInterface, nil
}

func deleteNetworkInterface(vpccli *vpc.VPCClient, interfaceId string) {
	req := vpccli.NewDeleteNetworkInterfaceRequest()
	req.InterfaceId = ucloud.String(interfaceId)
	resp, err := vpccli.DeleteNetworkInterface(req)
	if err != nil {
		ulog.Errorf("DeleteNetworkInterface from unetwork api service error: %v, UNI might leak", err)
	}
	if resp.RetCode != 0 {
		ulog.Errorf("DeleteNetworkInterface from unetwork api error %d: %s, UNI might leak", resp.RetCode, resp.Message)
	}
	ulog.Infof("Delete UNI %s success, %s", interfaceId, resp.GetRequestUUID())
}

func attachNetworkInterface(vpccli *vpc.VPCClient, interfaceId, instanceId string) error {
	req := vpccli.NewAttachNetworkInterfaceRequest()
	req.InterfaceId = ucloud.String(interfaceId)
	req.InstanceId = ucloud.String(instanceId)
	resp, err := vpccli.AttachNetworkInterface(req)
	if err != nil {
		return fmt.Errorf("AttachNetworkInterface from unetwork api service error: %v", err)
	}
	if resp.RetCode == 58205 {
		if uni, err := describeNetworkInterface(vpccli, interfaceId); err == nil {
			if uni.AttachInstanceId == instanceId {
				return nil
			}
		}
	}
	if resp.RetCode != 0 {
		return fmt.Errorf("AttachNetworkInterface from unetwork api error %d: %s", resp.RetCode, resp.Message)
	}
	ulog.Infof("Attach UNI %s to %s success", interfaceId, instanceId)
	return nil
}

func detachNetworkInterface(vpccli *vpc.VPCClient, interfaceId, instanceId string) error {

	req := vpccli.NewDetachNetworkInterfaceRequest()
	req.InterfaceId = ucloud.String(interfaceId)
	req.InstanceId = ucloud.String(instanceId)
	resp, err := vpccli.DetachNetworkInterface(req)
	if err != nil {
		return fmt.Errorf("DetachNetworkInterface from unetwork api service error: %v", err)
	}
	if resp.RetCode != 0 && resp.RetCode != 58206 { // 58206 => already detached
		return fmt.Errorf("DetachNetworkInterface from unetwork api error %d: %s", resp.RetCode, resp.Message)
	}
	ulog.Infof("Detach UNI %s from %s success", interfaceId, instanceId)
	return nil
}

func describeNetworkInterface(vpccli *vpc.VPCClient, interfaceId string) (*vpc.NetworkInterface, error) {
	req := vpccli.NewDescribeNetworkInterfaceRequest()
	req.InterfaceId = []string{interfaceId}
	resp, err := vpccli.DescribeNetworkInterface(req)
	if err != nil {
		return nil, fmt.Errorf("DescribeNetworkInterface from unetwork api service error: %v", err)
	}
	if resp.RetCode != 0 {
		return nil, fmt.Errorf("DescribeNetworkInterface from unetwork api error %d: %s", resp.RetCode, resp.Message)
	}
	if len(resp.NetworkInterfaceSet) == 0 {
		return nil, fmt.Errorf("DescribeNetworkInterface %s returned empty NetworkInterfaceSet", interfaceId)
	}
	return &resp.NetworkInterfaceSet[0], nil
}

func checkSubnetRemainsIP(vpccli *vpc.VPCClient, vpc, subnet string) (bool, error) {
	req := vpccli.NewDescribeSubnetRequest()
	req.ShowAvailableIPs = ucloud.Bool(true)
	req.SubnetId = ucloud.String(subnet)
	req.VPCId = ucloud.String(vpc)

	resp, err := vpccli.DescribeSubnet(req)
	if err != nil {
		return false, fmt.Errorf("DescribeSubnet from unetwork api service error: %v", err)
	}

	if len(resp.DataSet) == 0 {
		return false, fmt.Errorf("DescribeSubnet %s returned empty DataSet", subnet)
	}

	subnetInfo := resp.DataSet[0]
	return subnetInfo.AvailableIPs > 0, nil
}

func checkSecondaryIPExist(ip, mac, subnet string) (bool, error) {
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
	req.SubnetId = ucloud.String(subnet)
	req.Zone = ucloud.String(uapi.AvailabilityZone())
	req.VPCId = ucloud.String(uapi.VPCID())
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
	exist, err := checkSecondaryIPExist(pNet.VPCIP, pNet.MacAddress, pNet.SubnetID)
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

	objectId := pNet.InterfaceID
	if len(objectId) == 0 {
		objectId, err = uapi.GetObjectIDForSecondaryIP()
		if err != nil {
			objectId = client.InstanceID()
		}
	}

	req := cli.NewDeleteSecondaryIpRequest()
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
