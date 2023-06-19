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
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	crdclientset "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/clientset/versioned"
	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/kubeclient"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	"github.com/boltdb/bolt"
	"github.com/vishvananda/netlink"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
)

const (
	IpamdServiceSocket   = "/run/cni-vpc-ipamd.sock"
	NetworkDBName        = "cni-vpc-network"
	PoolDBName           = "cni-vpc-ip-pool"
	CooldownDBName       = "cni-vpc-cooldown"
	DefaultListenTCPPort = 7312
)

type ipamServer struct {
	kubeClient *kubernetes.Clientset
	crdClient  *crdclientset.Clientset

	// bolt db file handler
	dbHandler *bolt.DB

	networkDB  database.Database[rpc.PodNetwork]
	poolDB     database.Database[rpc.PodNetwork]
	cooldownDB database.Database[CooldownItem]

	nodeName string

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

	// The tcp address to listen
	tcpAddr string

	uapi *uapi.ApiClient
}

func Start() error {
	kubeClient, err := kubeclient.GetNodeClient()
	if err != nil {
		return err
	}
	crdClient, err := kubeclient.GetCRD()
	if err != nil {
		return err
	}

	uapiClient, err := uapi.NewClient()
	if err != nil {
		return fmt.Errorf("failed to init uapi client: %v", err)
	}

	server := grpc.NewServer()
	ipd := &ipamServer{
		kubeClient: kubeClient,
		crdClient:  crdClient,

		uapi: uapiClient,

		nodeName: os.Getenv("KUBE_NODE_NAME"),
	}
	ipd.initServer()
	// Enable telemetry
	rpc.RegisterCNIIpamServer(server, ipd)
	ulog.Infof("Start ipamd on node %v %v, kubernetes version: %v", os.Getenv("KUBE_NODE_NAME"), ipd.hostId, ipd.k8sVersion)

	go cleanUpOnTermination(server, ipd)

	if pathExist(IpamdServiceSocket) {
		os.Remove(IpamdServiceSocket)
	}

	go ipd.ipPoolWatermarkManager()
	go ipd.reconcile()

	// UPHost doesn't support uni, no need to run device plugin.
	// Type O/OS doesn't support uni, no need to run device plugin.
	if ipd.uniEnabled(os.Getenv("KUBE_NODE_NAME")) {
		go func() {
			err = startDevicePlugin()
			if err != nil {
				ulog.Fatalf("Start device plugin for UNI error: %v", err)
			}
		}()
	}

	socketListenr, err := net.Listen("unix", IpamdServiceSocket)
	ulog.Flush()
	if err != nil {
		ulog.Fatalf("listen socket: %v", err)
	}

	tcpListener, err := net.Listen("tcp", ipd.tcpAddr)
	if err != nil {
		ulog.Fatalf("listen tcp: %v", err)
	}

	errChan := make(chan error)
	go func() {
		ulog.Infof("Start to serve socket: %s", IpamdServiceSocket)
		err = server.Serve(socketListenr)
		errChan <- err
	}()
	go func() {
		ulog.Infof("Start to serve tcp: %s", ipd.tcpAddr)
		err = server.Serve(tcpListener)
		errChan <- err
	}()

	err = <-errChan
	return fmt.Errorf("failed to server: %v", err)
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
	k8sVersion, err := s.kubeClient.DiscoveryClient.ServerVersion()
	if err != nil {
		ulog.Fatalf("Cannot get k8s apiserver version, %v", err)
	}
	s.k8sVersion = k8sVersion.String()
	// About node itself
	hostId, err := s.getKubeNodeLabel(os.Getenv("KUBE_NODE_NAME"), KubeNodeLabelUhostID)
	if err != nil {
		ulog.Fatalf("Cannot get host id for node %v", os.Getenv("KUBE_NODE_NAME"))
	}
	s.hostId = hostId
	zoneId, err := s.getKubeNodeLabel(os.Getenv("KUBE_NODE_NAME"), KubeNodeZoneKey)
	if err != nil {
		zoneId, err = s.getKubeNodeLabel(os.Getenv("KUBE_NODE_NAME"), KubeNodeZoneTopologyKey)
		if err != nil {
			ulog.Fatalf("Cannot get zone id for node %v", os.Getenv("KUBE_NODE_NAME"))
		}
	}
	s.zoneId = zoneId
	// Fetch node's master network device mac address
	macAddr, err := iputils.GetNodeMacAddress("")
	if err != nil {
		ulog.Fatalf("Cannot get node master network interface mac addr, %v", err)
	}
	s.hostMacAddr = macAddr
	// Fetch node's master network device ip address
	nodeIp, err := iputils.GetNodeIPAddress("")
	if err != nil {
		ulog.Fatalf("Cannot get node IP address, %v", err)
	} else {
		s.nodeIpAddr = nodeIp
	}

	ip := s.nodeIpAddr.IP.String()
	s.tcpAddr = os.Getenv("LISTEN_TCP_ADDR")
	if s.tcpAddr == "" {
		s.tcpAddr = fmt.Sprintf("%s:%d", ip, DefaultListenTCPPort)
	}

	s.dbHandler, err = database.BoltHandler(storageFile)
	if err != nil {
		ulog.Fatalf("Create boltdb file handler for %s error: %v", storageFile, err)
	}
	s.networkDB, err = database.NewBolt[rpc.PodNetwork](NetworkDBName, s.dbHandler)
	if err != nil {
		ulog.Fatalf("Init network database error: %v", err)
	}
	s.poolDB, err = database.NewBolt[rpc.PodNetwork](PoolDBName, s.dbHandler)
	if err != nil {
		ulog.Fatalf("Init vpc ip pool database error: %v", err)
	}
	s.cooldownDB, err = database.NewBolt[CooldownItem](CooldownDBName, s.dbHandler)
	if err != nil {
		ulog.Fatalf("Init cooldown database error: %v", err)
	}

	clusterInfo, err := s.uapiListUK8SCluster()
	if err != nil {
		ulog.Errorf("List uk8s clusterInfo error: %v", err)
	} else {
		_, svcCIDR, err := net.ParseCIDR(clusterInfo.ServiceCIDR)
		if err != nil {
			ulog.Errorf("Parse svc cidr %s failed, %v", clusterInfo.ServiceCIDR, err)
			s.svcCIDR = nil
		} else {
			s.svcCIDR = svcCIDR
		}
	}
}

// Remove socket file on my termination.
// Remove any pre-allocated secondary vpc ip.
func cleanUpOnTermination(s *grpc.Server, ipd *ipamServer) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, os.Kill, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	ulog.Infof("Receive signal %+v, will stop myself gracefully", sig)
	chanStopLoop <- true

	err := ipd.dbHandler.Close()
	if err != nil {
		ulog.Errorf("Close boltdb handler error: %v", err)
	}

	s.Stop()

	ulog.Infof("Good Bye!")
	ulog.Flush()
	os.Exit(0)
}
