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
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	v1beta1 "github.com/ucloud/uk8s-cni-vpc/pkg/generated/clientset/versioned/typed/vipcontroller/v1beta1"
	"github.com/ucloud/uk8s-cni-vpc/pkg/rpc"
	"github.com/ucloud/uk8s-cni-vpc/pkg/storage"

	"github.com/boltdb/bolt"
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	IpamdServiceSocket    = "/run/cni-vpc-ipamd.sock"
	UHostMasterInterface  = "eth0"
	UPHostMasterInterface = "net1"
	CNIVpcDbName          = "cni-vpc-network"
	CNIVPCIpPoolDBName    = "cni-vpc-ip-pool"
)

type ipamServer struct {
	k8s       *kubernetes.Clientset
	vipclient *v1beta1.VipcontrollerV1beta1Client
	// bolt db file handler
	db *bolt.DB
	// *rpc.PodNetwork
	store storage.Storage[rpc.PodNetwork]
	// *vpc.IpInfo
	pool     storage.Storage[rpc.PodNetwork]
	nodeName string

	cooldownSet  []*cooldownIPItem
	cooldownLock sync.Mutex

	conflictLock sync.Mutex

	unschedulable bool

	// UHostId, UPHostId ...
	hostId string
	// eth0 for UHost, eth1 for UPHost
	hostMacAddr string
	zoneId      string
	k8sVersion  string
	svcCIDR     *net.IPNet
	nodeIpAddr  *netlink.Addr

	assignLock sync.RWMutex
}

func IpamdServer() error {
	server := grpc.NewServer()
	ipd := &ipamServer{
		k8s:       getK8sClient(),
		vipclient: getVipClient(),

		nodeName: os.Getenv("KUBE_NODE_NAME"),
	}
	ipd.initServer()
	// Enable telemetry
	rpc.RegisterCNIIpamServer(server, ipd)
	klog.Infof("Start ipamd on node %v %v, kubernetes version: %v", os.Getenv("KUBE_NODE_NAME"), ipd.hostId, ipd.k8sVersion)

	go cleanUpOnTermination(server, ipd)

	if pathExist(IpamdServiceSocket) {
		os.Remove(IpamdServiceSocket)
	}
	listener, err := net.Listen("unix", IpamdServiceSocket)
	klog.Flush()
	if err != nil {
		klog.Fatal(err)
	}

	go ipd.ipPoolWatermarkManager()
	go ipd.reconcile()

	// UPHost doesn't support uni, no need to run device plugin.
	// Type O/OS doesn't support uni, no need to run device plugin.
	if ipd.uniEnabled(os.Getenv("KUBE_NODE_NAME")) {
		go func() {
			err := startDevicePlugin()
			if err != nil {
				klog.Fatalf("Cannot start device plugin for UNI: %v", err)
			}
		}()
	}

	return server.Serve(listener)
}

func (s *ipamServer) uniEnabled(nodeName string) bool {
	instanceType, err := s.getKubeNodeLabel(nodeName, KubeNodeInstanceTypeKey)
	if err != nil {
		return false
	}
	machineType, err := s.getKubeNodeLabel(nodeName, KubeNodeMachineTypeKey)
	if err != nil {
		return false
	}

	if instanceType == "uhost" {
		if machineType != "O" && machineType != "OS" {
			return true
		}
	}
	return false
}

func (s *ipamServer) initServer() {
	// About k8s version
	k8sVersion, err := s.k8s.DiscoveryClient.ServerVersion()
	if err != nil {
		klog.Fatalf("Cannot get k8s apiserver version, %v", err)
	}
	s.k8sVersion = k8sVersion.String()
	// About node itself
	hostId, err := s.getKubeNodeLabel(os.Getenv("KUBE_NODE_NAME"), KubeNodeLabelUhostID)
	if err != nil {
		klog.Fatalf("Cannot get host id for node %v", os.Getenv("KUBE_NODE_NAME"))
	}
	s.hostId = hostId
	zoneId, err := s.getKubeNodeLabel(os.Getenv("KUBE_NODE_NAME"), KubeNodeZoneKey)
	if err != nil {
		zoneId, err = s.getKubeNodeLabel(os.Getenv("KUBE_NODE_NAME"), KubeNodeZoneTopologyKey)
		if err != nil {
			klog.Fatalf("Cannot get zone id for node %v", os.Getenv("KUBE_NODE_NAME"))
		}
	}
	s.zoneId = zoneId
	// Fetch node's master network device mac address
	masterInterface := getMasterInterface()
	macAddr, err := getNodeMacAddress(masterInterface)
	if err != nil {
		klog.Fatalf("Cannot get node master network interface mac addr, %v", err)
	}
	s.hostMacAddr = macAddr
	// Fetch node's master network device ip address
	nodeIp, err := getNodeIPAddress(masterInterface)
	if err != nil {
		klog.Errorf("Cannot get node master network interface mac addr, %v", err)
		s.nodeIpAddr = nil
	} else {
		s.nodeIpAddr = nodeIp
	}

	s.db, err = storage.NewDBFileHandler(storageFile)
	if err != nil {
		klog.Fatalf("cannot get boltdb file  handler for %s: %v", storageFile, err)
	}
	s.store, err = storage.NewDisk[rpc.PodNetwork](CNIVpcDbName, s.db)
	if err != nil {
		klog.Fatalf("cannot get pod network storage handler: %v", err)
	}
	s.pool, err = storage.NewDisk[rpc.PodNetwork](CNIVPCIpPoolDBName, s.db)
	if err != nil {
		klog.Fatalf("cannot get vpc ip pool storage handler: %v", err)
	}

	clusterInfo, err := s.uapiListUK8SCluster()
	if err != nil {
		klog.Errorf("Cannot list uk8s clusterInfo, %v", err)
	} else {
		_, svcCIDR, err := net.ParseCIDR(clusterInfo.ServiceCIDR)
		if err != nil {
			klog.Errorf("Parse svc cidr %s failed, %v", clusterInfo.ServiceCIDR, err)
			s.svcCIDR = nil
		} else {
			s.svcCIDR = svcCIDR
		}
	}
}

// Remove socket file on my termination.
// Remove any pre-allocated secondary vpc ip.
func cleanUpOnTermination(s *grpc.Server, ipd *ipamServer) {
	quit := make(chan os.Signal)
	signal.Notify(quit, os.Interrupt, os.Kill, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	klog.Infof("Recieve signal %+v, will stop myself gracefully", sig)
	chanStopLoop <- true
	ipd.doFreeIpPool()
	ipd.doFreeCooldown()
	ipd.store.Close()
	ipd.pool.Close()
	//if ipamd is updating and rollback to cni, the cni will not know how to deal with the static ip.(maybe free the static ip)
	//so unless the user detach the ipamd through another way, the ipamd should not unInstallCNIComponent
	//unInstallCNIComponent()
	klog.Info("Good Bye!")
	s.Stop()
	klog.Flush()
	os.Exit(0)
}

func unInstallCNIComponent() {
	err := GenerateConfFile(false)
	if err != nil {
		klog.Fatal(err)
	}
	// Install cni binary and configure file
	err = InstallCNIComponent("/app/10-cnivpc.conf", "/opt/cni/net.d/10-cnivpc.conf")
	if err != nil {
		klog.Errorf("Failed to copy 10-cnivpc.conf, %v", err)
	}
}
