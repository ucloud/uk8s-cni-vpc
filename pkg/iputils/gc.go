package iputils

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"time"

	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/kubeclient"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/kubernetes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

const (
	gcDataPath = "/opt/cni/gcinfo"

	maxSweep = 5

	GCCollectSeconds int64 = 120

	defaultLifetime = 10
)

type GarbageCollectData struct {
	LastUpdate     int64 `yaml:"last_update"`
	DisableSweep   bool  `yaml:"disable_sweep"`
	DisableCollect bool  `yaml:"disable_collect"`

	Pods   []*PodIP    `yaml:"pods"`
	Pool   []string    `yaml:"pool"`
	Unused []*UnusedIP `yaml:"unused"`
}

func sortUnusedData(unused []*UnusedIP) {
	sort.Slice(unused, func(i, j int) bool {
		return unused[i].Lifetime < unused[j].Lifetime
	})
}

type PodIP struct {
	Namespace string `yaml:"namespace"`
	Name      string `yaml:"name"`
	IP        string `yaml:"ip"`
}

type UnusedIP struct {
	Lifetime int    `yaml:"lifetime"`
	IP       string `yaml:"ip"`
}

type GarbageCollector struct {
	data *GarbageCollectData

	node string
	mac  string

	uapiClient *uapi.ApiClient

	kubeClient *kubernetes.Clientset

	force bool
}

func NewGC(uapiClient *uapi.ApiClient, node, mac string) (*GarbageCollector, error) {
	_, err := os.Stat(gcDataPath)
	if os.IsNotExist(err) {
		return EmptyGC(uapiClient, node, mac), nil
	}

	file, err := os.Open(gcDataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open gc data path: %v", err)
	}
	defer file.Close()

	var data GarbageCollectData
	err = yaml.NewDecoder(file).Decode(&data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode gc data: %v", err)
	}

	return &GarbageCollector{
		uapiClient: uapiClient,
		data:       &data,

		node: node,
		mac:  mac,
	}, nil
}

func EmptyGC(uapiClient *uapi.ApiClient, node, mac string) *GarbageCollector {
	return &GarbageCollector{
		data:       new(GarbageCollectData),
		uapiClient: uapiClient,

		node: node,
		mac:  mac,
	}
}

func (gc *GarbageCollector) Force() {
	gc.force = true
}

func (gc *GarbageCollector) NeedCollect() bool {
	now := time.Now().Unix()
	nextCollect := gc.data.LastUpdate + GCCollectSeconds
	return now >= nextCollect
}

func (gc *GarbageCollector) SetKubeClient(client *kubernetes.Clientset) {
	gc.kubeClient = client
}

func (gc *GarbageCollector) getUnusedIPs() []string {
	ips := make([]string, len(gc.data.Unused))
	for i, unused := range gc.data.Unused {
		ips[i] = unused.IP
	}
	return ips
}

func (gc *GarbageCollector) Run(collect bool, pool []string) error {
	if collect {
		beforeUnused := gc.getUnusedIPs()
		err := gc.Collect(pool)
		if err != nil {
			return fmt.Errorf("failed to run gc collect: %v", err)
		}

		afterUnused := gc.getUnusedIPs()
		if !reflect.DeepEqual(beforeUnused, afterUnused) {
			ulog.Infof("GC Collect update unused from %v to %v", beforeUnused, afterUnused)
		}
	}
	err := gc.Sweep()
	if err != nil {
		return fmt.Errorf("failed to run gc sweep: %v", err)
	}
	return nil
}

func (gc *GarbageCollector) Collect(pool []string) error {
	if !gc.force && gc.data.DisableCollect {
		return nil
	}
	podIPs, err := gc.collectPodIPs()
	if err != nil {
		return fmt.Errorf("failed to collect pod IPs: %v", err)
	}

	nodeIPs, err := gc.collectNodeIPs()
	if err != nil {
		return fmt.Errorf("failed to collect node IPs: %v", err)
	}

	usedIPs := make(map[string]struct{}, len(podIPs)+len(pool))
	for _, ip := range pool {
		usedIPs[ip] = struct{}{}
	}
	for _, podIP := range podIPs {
		usedIPs[podIP.IP] = struct{}{}
	}

	lastUnused := make(map[string]int, len(gc.data.Unused))
	for _, unsed := range gc.data.Unused {
		lastUnused[unsed.IP] = unsed.Lifetime
	}

	unused := make([]*UnusedIP, 0)
	for _, nodeIP := range nodeIPs {
		if _, ok := usedIPs[nodeIP]; ok {
			continue
		}
		var lifetime int
		lastLifetime, ok := lastUnused[nodeIP]
		if !ok {
			lifetime = defaultLifetime
		} else {
			lifetime = lastLifetime - 1
		}
		if lifetime < 0 {
			lifetime = -1
		}

		unused = append(unused, &UnusedIP{
			Lifetime: lifetime,
			IP:       nodeIP,
		})
	}
	sortUnusedData(unused)

	data := &GarbageCollectData{
		LastUpdate: time.Now().Unix(),

		DisableSweep:   gc.data.DisableSweep,
		DisableCollect: gc.data.DisableCollect,

		Pods:   podIPs,
		Pool:   pool,
		Unused: unused,
	}

	gc.data = data
	return gc.saveData()
}

func (gc *GarbageCollector) collectPodIPs() ([]*PodIP, error) {
	var err error
	if gc.kubeClient == nil {
		gc.kubeClient, err = kubeclient.GetNodeClient()
		if err != nil {
			return nil, fmt.Errorf("failed to init node kube client: %v", err)
		}
	}

	ctx := context.Background()
	nodeList, err := gc.kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list node: %v", err)
	}
	found := false
	for _, node := range nodeList.Items {
		if node.Name == gc.node {
			found = true
			break
		}
	}
	if !found {
		// Use this to prevent the user from manually changing the nodeName,
		// and the IP cannot be matched successfully.
		// Without this step, the PodIP on the node will be marked by mistake.
		return nil, fmt.Errorf("cannot find node %s", gc.node)
	}

	opts := metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", gc.node).String(),
		ResourceVersion: "0",
	}
	podList, err := gc.kubeClient.CoreV1().Pods(corev1.NamespaceAll).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	if len(podList.Items) == 0 {
		// Double check, in UK8S, each Node has at least one Pod.
		return nil, errors.New("unexpected pod length, should have at least one pod")
	}

	ips := make([]*PodIP, 0, len(podList.Items))
	ipSet := make(map[string]struct{}, len(podList.Items))
	for _, pod := range podList.Items {
		if pod.Spec.HostNetwork {
			continue
		}
		podIP := pod.Status.PodIP
		if _, ok := ipSet[podIP]; ok {
			continue
		}
		ips = append(ips, &PodIP{
			Namespace: pod.Namespace,
			Name:      pod.Name,
			IP:        podIP,
		})
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("unexpected ip length, should have at least one ip")
	}
	return ips, nil
}

func (gc *GarbageCollector) collectNodeIPs() ([]string, error) {
	vpcClient, err := gc.uapiClient.VPCClient()
	if err != nil {
		return nil, err
	}

	req := vpcClient.NewDescribeSecondaryIpRequest()
	req.VPCId = ucloud.String(gc.uapiClient.VPCID())
	req.SubnetId = ucloud.String(gc.uapiClient.SubnetID())
	req.Mac = ucloud.String(gc.mac)

	resp, err := vpcClient.DescribeSecondaryIp(req)
	if err != nil {
		return nil, fmt.Errorf("failed to describe secondary ip: %v", err)
	}

	ips := make([]string, 0, len(resp.DataSet))
	for _, item := range resp.DataSet {
		ips = append(ips, item.Ip)
	}
	return ips, nil
}

func (gc *GarbageCollector) Sweep() error {
	if !gc.force && gc.data.DisableSweep {
		return nil
	}
	if len(gc.data.Unused) == 0 {
		return nil
	}

	vpcClient, err := gc.uapiClient.VPCClient()
	if err != nil {
		return err
	}

	var live []*UnusedIP
	var deleted []string
	var cnt int
	for _, unused := range gc.data.Unused {
		if cnt >= maxSweep || unused.Lifetime >= 0 {
			live = append(live, unused)
			continue
		}
		cnt++

		req := vpcClient.NewDeleteSecondaryIpRequest()
		req.VPCId = ucloud.String(gc.uapiClient.VPCID())
		req.SubnetId = ucloud.String(gc.uapiClient.SubnetID())
		req.Ip = ucloud.String(unused.IP)
		req.Mac = ucloud.String(gc.mac)

		_, err = vpcClient.DeleteSecondaryIp(req)
		if err != nil {
			// Do not abort the sweep process if releasing the IP fails.
			// These IPs will be rediscovered in the next collect process.
			ulog.Warnf("GC Sweep failed to delete secondary ip %s: %v", unused.IP, err)
			continue
		}
		deleted = append(deleted, unused.IP)
	}
	if len(deleted) == 0 {
		return nil
	}

	ulog.Infof("GC Sweep deleted ip: %v", deleted)
	gc.data.Unused = live
	err = gc.saveData()
	if err != nil {
		return fmt.Errorf("failed to save data: %v", err)
	}

	return nil
}

func (gc *GarbageCollector) saveData() error {
	file, err := os.OpenFile(gcDataPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open gc data file: %v", err)
	}
	defer file.Close()

	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	err = encoder.Encode(gc.data)
	if err != nil {
		return fmt.Errorf("failed to encode gc data: %v", err)
	}
	return nil
}
