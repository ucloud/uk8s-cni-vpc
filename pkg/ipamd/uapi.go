package ipamd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/ucloud/ucloud-sdk-go/services/uk8s"
	"github.com/ucloud/ucloud-sdk-go/services/unet"
	"github.com/ucloud/ucloud-sdk-go/services/vpc"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/arping"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
)

const (
	ResourceTypeUNI    = "uni"
	UAPIErrorIPNotExst = 58221

	instanceTypeCube    = "Cube"
	instanceTypeUHost   = "UHost"
	instanceTypeUPHost  = "UPM"
	instanceTypeUDocker = "UDocker"
	instanceTypeUDHost  = "UDHost"
	instanceTypeUNI     = "UNI"
)

var payModeMap map[string]string = map[string]string{
	"traffic":        "Traffic",
	"bandwidth":      "Bandwidth",
	"sharebandwidth": "ShareBandwidth",
}

func getPayMode(paymode string) string {
	if val, found := payModeMap[strings.ToLower(paymode)]; found {
		return val
	}
	return "Traffic"
}

var chargeTypeMap map[string]string = map[string]string{
	"year":    "Year",
	"month":   "Month",
	"dynamic": "Dynamic",
	"trial":   "Trial",
}

func getChargeType(chargeType string) string {
	if val, found := chargeTypeMap[strings.ToLower(chargeType)]; found {
		return val
	}
	return "Month"
}

var ispMap map[string]string = map[string]string{
	// International
	"hk":           "International",
	"us-ca":        "International",
	"us-ws":        "International",
	"tw-tp":        "International",
	"tw-tp2":       "International",
	"tw-kh":        "International",
	"kr-seoul":     "International",
	"th-bkk":       "International",
	"vn-sng":       "International",
	"sg":           "International",
	"rus-mosc":     "International",
	"jpn-tky":      "International",
	"uae-dubai":    "International",
	"idn-jakarta":  "International",
	"ind-mumbai":   "International",
	"ge-fra":       "International",
	"uk-london":    "International",
	"bra-saopaulo": "International",
	"afr-nigeria":  "International",
	"ph-mnl":       "International",
	// Domestic
	"cn-inspur":  "Bgp",
	"cn-qz":      "Bgp",
	"cn-inspur2": "Bgp",
	"cn-xj":      "Bgp",
	"cn-zj":      "Bgp",
	"cn-sh":      "Bgp",
	"cn-sh1":     "Bgp",
	"cn-sh2":     "Bgp",
	"cn-sh3":     "Bgp",
	"cn-bj1":     "Bgp",
	"cn-bj2":     "Bgp",
	"cn-gd":      "Bgp",
	"cn-gd2":     "Bgp",
	"cn-hz":      "Bgp",
	"cn-cmccit1": "Bgp",
	"cn-cmccit2": "Bgp",
}

func getRegionISP() string {
	region := uapi.LocalRegion()
	if val, found := ispMap[region]; found {
		return val
	} else {
		if strings.HasPrefix(region, "cn-") {
			return "Bgp"
		}
	}
	return "International"
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

func getObjectIDforSecondaryIp() (string, error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return "", err
	}
	instanceId := uApi.InstanceID()
	if instanceType(instanceId) != instanceTypeUHost {
		return instanceId, nil
	}

	req := uApi.UHostClient().NewDescribeUHostInstanceRequest()
	req.UHostIds = []string{}
	resp, err := uApi.UHostClient().DescribeUHostInstance(req)
	if err != nil || len(resp.UHostSet) == 0 {
		klog.Errorf("DescribeUHostInstance for %v failed, %v", instanceId, err)
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

func (s *ipamServer) uapiAllocateSecondaryIp(number int) (ips []*vpc.IpInfo, err error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return nil, err
	}
	req := uApi.VPCClient().NewAllocateSecondaryIpRequest()
	req.Mac = ucloud.String(s.hostMacAddr)
	ObjectId, err := getObjectIDforSecondaryIp()
	if err != nil {
		ObjectId = s.hostId
	}
	req.ObjectId = ucloud.String(ObjectId)
	req.Zone = ucloud.String(s.zoneId)
	req.VPCId = ucloud.String(uApi.VPCID())
	req.SubnetId = ucloud.String(uApi.SubnetID())

	for i := 0; i < number; i++ {
		resp, err := uApi.VPCClient().AllocateSecondaryIp(req)
		if err != nil {
			klog.Errorf("Failed to invoke API AllocateSecondaryIp, response id %v, err %v", resp.GetRequestUUID(), err)
			continue
		}

		klog.Infof("Allocated Ip %v from unetwork api service", resp.IpInfo.Ip)
		ips = append(ips, &(resp.IpInfo))
	}
	return
}

func (s *ipamServer) checkIPConflict(ip string) error {
	s.conflictLock.Lock()
	defer s.conflictLock.Unlock()
	klog.Infof("Begin to detect ip conflict for %s", ip)
	start := time.Now()
	conflict, err := arping.DetectIpConflictWithGratuitousArp(net.ParseIP(ip), getMasterInterface())
	if err != nil {
		return fmt.Errorf("failed to detect conflict for ip %s: %v", ip, err)
	}
	if conflict {
		return fmt.Errorf("ip %s is still in conflict after retrying", ip)
	}
	klog.Infof("Detect ip conflict for %s done, took %v", ip, time.Since(start))
	return nil
}

func (s *ipamServer) uapiAllocateSpecifiedSecondaryIp(ip, subnet string) (ipInfo *vpc.IpInfo, err error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return nil, err
	}
	req := uApi.VPCClient().NewAllocateSecondaryIpRequest()
	req.Mac = ucloud.String(s.hostMacAddr)
	req.Ip = ucloud.String(ip) //指定IP创建
	ObjectId, err := getObjectIDforSecondaryIp()
	if err != nil {
		ObjectId = s.hostId
	}
	req.ObjectId = ucloud.String(ObjectId)
	req.Zone = ucloud.String(s.zoneId)
	req.VPCId = ucloud.String(uApi.VPCID())
	req.SubnetId = ucloud.String(subnet)

	resp, err := uApi.VPCClient().AllocateSecondaryIp(req)
	if err != nil {
		klog.Errorf("Failed to invoke API AllocateSecondaryIp, response id %v, err %v", resp.GetRequestUUID(), err)
	}
	klog.Infof("Allocated Ip %v from unetwork api service", resp.IpInfo.Ip)
	return &resp.IpInfo, nil
}

func (s *ipamServer) uapiDescribeSecondaryIp(ip, subnetId string) (*vpc.IpInfo, error) {
	uApi, err := uapi.NewApiClient()
	client := uApi.VPCClient()
	req := client.NewDescribeSecondaryIpRequest()

	req.Ip = ucloud.String(ip)
	req.SubnetId = ucloud.String(subnetId)
	req.VPCId = ucloud.String(uApi.VPCID())
	resp, err := uApi.VPCClient().DescribeSecondaryIp(req)
	if err != nil {
		klog.Errorf("Describe secondaryIp %s failed, request id %s, %v", ip, resp.GetRequestUUID(), err)
		return nil, err
	}

	if len(resp.DataSet) == 0 {
		return nil, nil
	}
	return &(resp.DataSet[0]), nil
}

func (s *ipamServer) uapiMoveSecondaryIPMac(ip, prevMac, dstMac, subnetId string) error {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return err
	}
	req := uApi.VPCClient().NewMoveSecondaryIPMacRequest()
	req.Ip = ucloud.String(ip)
	req.NewMac = ucloud.String(dstMac)
	req.OldMac = ucloud.String(prevMac)
	req.SubnetId = ucloud.String(subnetId)
	_, err = uApi.VPCClient().MoveSecondaryIPMac(req)

	return err
}

func (s *ipamServer) checkSecondaryIpExist(ip, mac string) (bool, error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return false, err
	}
	req := uApi.VPCClient().NewDescribeSecondaryIpRequest()
	req.Ip = ucloud.String(ip)
	req.Mac = ucloud.String(mac)
	req.Zone = ucloud.String(uApi.AvailabilityZone())
	req.VPCId = ucloud.String(uApi.VPCID())
	req.SubnetId = ucloud.String(uApi.SubnetID())
	resp, err := uApi.VPCClient().DescribeSecondaryIp(req)
	if err != nil {
		klog.Errorf("DescribeSecondaryIp %s failed, request id %s, %v", ip, resp.GetRequestUUID(), err)
		return false, err
	}
	if len(resp.DataSet) > 0 {
		return true, nil
	}
	return false, nil
}

func (s *ipamServer) uapiDeleteSecondaryIp(ip string) error {
	exist, err := s.checkSecondaryIpExist(ip, s.hostMacAddr)
	if err != nil {
		return fmt.Errorf("cannot find secondary ip %s, %v", ip, err)
	}
	if !exist {
		klog.Infof("Secondary Ip %s has already been deleted in previous cni command DEL", ip)
		return nil
	}

	uApi, err := uapi.NewApiClient()
	if err != nil {
		return err
	}
	req := uApi.VPCClient().NewDeleteSecondaryIpRequest()
	req.Zone = ucloud.String(s.zoneId)
	req.Mac = ucloud.String(s.hostMacAddr)
	req.Ip = ucloud.String(ip)
	req.ObjectId = ucloud.String(s.hostId)
	req.VPCId = ucloud.String(uApi.VPCID())
	req.SubnetId = ucloud.String(uApi.SubnetID())

	resp, err := uApi.VPCClient().DeleteSecondaryIp(req)
	if err == nil {
		klog.Infof("Secondary Ip %v deleted by unetwork api service", ip)
	}
	if resp.RetCode == UAPIErrorIPNotExst {
		klog.Warningf("Secondary ip %s has been deleted before", ip)
		return nil
	}
	return err
}

func (s *ipamServer) uapiDescribeFirewall(firewallId string) (*unet.FirewallDataSet, error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return nil, err
	}
	req := uApi.UNetClient().NewDescribeFirewallRequest()
	req.FWId = ucloud.String(firewallId)

	resp, err := uApi.UNetClient().DescribeFirewall(req)
	if err != nil {
		return nil, err
	}
	if len(resp.DataSet) == 0 {
		return nil, fmt.Errorf("firewall %s not found", firewallId)
	}
	return &(resp.DataSet[0]), nil
}

func (s *ipamServer) uapiCreateUNI(pod *v1.Pod, cfg *EIPCfg) (string, error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return "", err
	}
	req := uApi.VPCClient().NewCreateNetworkInterfaceRequest()
	req.VPCId = ucloud.String(uApi.VPCID())
	req.Name = ucloud.String(getUNIorEIPName(pod))
	if len(cfg.SecurityGroupId) > 0 {
		firewall, err := s.uapiDescribeFirewall(cfg.SecurityGroupId)
		if err != nil {
			return "", fmt.Errorf("cannot get firewall %s, %v", cfg.SecurityGroupId, err)
		}
		req.SecurityGroupId = ucloud.String(firewall.GroupId)
	}
	req.SubnetId = ucloud.String(uApi.SubnetID())
	req.Remark = ucloud.String(getUNetRemark(string(pod.UID)))
	resp, err := uApi.VPCClient().CreateNetworkInterface(req)
	if err != nil {
		return "", err
	}

	return resp.NetworkInterface.InterfaceId, nil
}

func (s *ipamServer) uapiDescribeUNI(uniId string) (*vpc.NetworkInterface, error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return nil, err
	}
	req := uApi.VPCClient().NewDescribeNetworkInterfaceRequest()
	req.InterfaceId = []string{uniId}
	resp, err := uApi.VPCClient().DescribeNetworkInterface(req)
	if err != nil {
		klog.Errorf("DescribeNetworkInterface %s failed, request id %s %v", uniId, resp.GetRequestUUID(), err)
		return nil, err
	}
	if len(resp.NetworkInterfaceSet) == 0 {
		return nil, fmt.Errorf("UNI %s not found", uniId)
	}
	return &(resp.NetworkInterfaceSet[0]), nil
}

func (s *ipamServer) uapiAttachUNI(uhostId, uniId string) error {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return err
	}
	req := uApi.VPCClient().NewAttachNetworkInterfaceRequest()
	req.InterfaceId = ucloud.String(uniId)
	req.InstanceId = ucloud.String(uhostId)
	_, err = uApi.VPCClient().AttachNetworkInterface(req)
	return err
}

func (s *ipamServer) uapiDetachUNI(uhostId, uniId string) error {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return err
	}
	req := uApi.VPCClient().NewDetachNetworkInterfaceRequest()
	req.InstanceId = ucloud.String(uhostId)
	req.InterfaceId = ucloud.String(uniId)
	_, err = uApi.VPCClient().DetachNetworkInterface(req)

	return err
}

func (s *ipamServer) uapiDeleteUNI(uniId string) error {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return err
	}

	req := uApi.VPCClient().NewDeleteNetworkInterfaceRequest()
	req.InterfaceId = ucloud.String(uniId)
	_, err = uApi.VPCClient().DeleteNetworkInterface(req)
	return err
}

func getUNetRemark(uid string) string {
	if len(os.Getenv("UCLOUD_UK8S_CLUSTER_ID")) > 0 {
		return os.Getenv("UCLOUD_UK8S_CLUSTER_ID") + "-" + uid
	} else {
		return uid
	}
}

func (s *ipamServer) uapiDescribeEIP(eipId string) (*unet.UnetEIPSet, error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return nil, err
	}
	req := uApi.UNetClient().NewDescribeEIPRequest()
	req.EIPIds = []string{eipId}
	req.Limit = ucloud.Int(1)
	req.Offset = ucloud.Int(0)
	resp, err := uApi.UNetClient().DescribeEIP(req)
	if err != nil {
		return nil, err
	}
	if len(resp.EIPSet) == 0 {
		return nil, fmt.Errorf("EIP for %s not found", eipId)
	}
	return &(resp.EIPSet[0]), nil
}

func getUNIorEIPName(pod *v1.Pod) (name string) {
	if len(os.Getenv("UCLOUD_UK8S_CLUSTER_ID")) > 0 {
		name = pod.Name + "." + pod.Namespace + "." + os.Getenv("UCLOUD_UK8S_CLUSTER_ID")
	} else {
		name = pod.Name + "." + pod.Namespace
	}
	if len(name) > 64 {
		name = name[0:63]
	}
	return
}

func (s *ipamServer) uapiAllocateEIP(pod *v1.Pod, cfg *EIPCfg) (*unet.UnetAllocateEIPSet, error) {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return nil, err
	}
	req := uApi.UNetClient().NewAllocateEIPRequest()
	req.Bandwidth = ucloud.Int(cfg.Bandwidth)
	req.Quantity = ucloud.Int(cfg.Quantity)
	req.Name = ucloud.String(getUNIorEIPName(pod))
	req.OperatorName = ucloud.String(getRegionISP())
	if len(cfg.PayMode) > 0 {
		req.PayMode = ucloud.String(getPayMode(cfg.PayMode))
	}
	if len(cfg.ChargeType) > 0 {
		req.ChargeType = ucloud.String(getChargeType(cfg.ChargeType))
	}
	if len(cfg.ShareBandwidthId) > 0 {
		req.ShareBandwidthId = ucloud.String(cfg.ShareBandwidthId)
	}

	req.Remark = ucloud.String(getUNetRemark(string(pod.UID)))

	resp, err := uApi.UNetClient().AllocateEIP(req)
	if err != nil {
		return nil, err
	}

	if len(resp.EIPSet) == 0 {
		return nil, errors.New("EIP is nil")
	}

	return &(resp.EIPSet[0]), nil
}

func (s *ipamServer) uapiReleaseEIP(eipId string) error {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return err
	}
	req := uApi.UNetClient().NewReleaseEIPRequest()
	req.EIPId = ucloud.String(eipId)
	_, err = uApi.UNetClient().ReleaseEIP(req)
	return err
}

func (s *ipamServer) uapiBindEIPForUNI(eipId, resId string) error {
	// Make sure eip is avaiable
	eipSet, err := s.uapiDescribeEIP(eipId)
	if err != nil {
		return fmt.Errorf("cannot describe eip %s before bind it to %s, %v", eipId, resId, err)
	}

	if eipSet.Status == "freeze" {
		return fmt.Errorf("eip %s is freeze, please contact UCloud support team.", eipId)
	} else if eipSet.Status == "used" && eipSet.Resource.ResourceID != resId {
		klog.Infof("EIP %s is bound to %s, unbind it now", eipId, eipSet.Resource.ResourceID)
		unbindId := eipSet.Resource.ResourceID
		unbindType := eipSet.Resource.ResourceType
		if len(eipSet.Resource.SubResourceId) > 0 {
			unbindId = eipSet.Resource.SubResourceId
		}
		if len(eipSet.Resource.SubResourceType) > 0 {
			unbindType = eipSet.Resource.SubResourceType
		}
		err = s.uapiUnbindEIP(eipId, unbindId, unbindType)
		if err != nil {
			return fmt.Errorf("cannot unbind eip %s from %s before rebind it to %s, %v", eipId, eipSet.Resource.ResourceID, resId, err)
		}
	}

	uApi, err := uapi.NewApiClient()
	if err != nil {
		return err
	}
	req := uApi.UNetClient().NewBindEIPRequest()
	req.EIPId = ucloud.String(eipId)
	req.ResourceType = ucloud.String(ResourceTypeUNI)
	req.ResourceId = ucloud.String(resId)
	_, err = uApi.UNetClient().BindEIP(req)
	return err
}

func (s *ipamServer) uapiUnbindEIP(eipId, resId, resType string) error {
	uApi, err := uapi.NewApiClient()
	if err != nil {
		return err
	}
	req := uApi.UNetClient().NewUnBindEIPRequest()
	req.EIPId = ucloud.String(eipId)
	req.ResourceType = ucloud.String(resType)
	req.ResourceId = ucloud.String(resId)
	_, err = uApi.UNetClient().UnBindEIP(req)
	return err
}

func (s *ipamServer) uapiListUK8SCluster() (*uk8s.ClusterSet, error) {
	clusterId := os.Getenv("UCLOUD_UK8S_CLUSTER_ID")
	if len(clusterId) == 0 {
		return nil, fmt.Errorf("cannot get cluster id by environment var UCLOUD_REGION_ID")
	}

	uApi, err := uapi.NewApiClient()
	if err != nil {
		return nil, err
	}
	req := uApi.UK8SClient().NewListUK8SClusterV2Request()
	req.ClusterId = ucloud.String(clusterId)
	resp, err := uApi.UK8SClient().ListUK8SClusterV2(req)
	if err != nil {
		return nil, err
	}
	if len(resp.ClusterSet) == 0 {
		return nil, errors.New("UK8S ClusterSet is empty")
	}

	return &(resp.ClusterSet[0]), nil
}
