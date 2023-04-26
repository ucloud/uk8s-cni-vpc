package iputils

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

const (
	gcDataPath = "/opt/cni/gcinfo"

	maxSweep = 5

	GCCollectSeconds int64 = 60 * 10 // 10min

	defaultLifetime = 10

	nodeKubeConfigPath = "/etc/kubernetes/kubelet.kubeconfig"
)

type GarbageCollectorData struct {
	LastUpdate     int64 `yaml:"last_update"`
	DisableSweep   bool  `yaml:"disable_sweep"`
	DisableCollect bool  `yaml:"disable_collect"`

	Pods   []*PodIP    `yaml:"pods"`
	Pool   []string    `yaml:"pool"`
	Unused []*UnusedIP `yaml:"unused"`
}

func sortUnusedData(unused []*UnusedIP) {
	sort.Slice(unused, func(i, j int) bool {
		return unused[i].Lifetime > unused[j].Lifetime
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
	data *GarbageCollectorData

	ip  string
	mac string

	uapiClient *uapi.ApiClient

	kubeClient *kubernetes.Clientset

	force bool
}

func NewGC(uapiClient *uapi.ApiClient, ip, mac string) (*GarbageCollector, error) {
	_, err := os.Stat(gcDataPath)
	if os.IsNotExist(err) {
		return EmptyGC(uapiClient, ip, mac), nil
	}

	file, err := os.Open(gcDataPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open gc data path: %v", err)
	}
	defer file.Close()

	var data GarbageCollectorData
	err = yaml.NewDecoder(file).Decode(&data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode gc data: %v", err)
	}

	return &GarbageCollector{
		uapiClient: uapiClient,
		data:       &data,

		ip:  ip,
		mac: mac,
	}, nil
}

func EmptyGC(uapiClient *uapi.ApiClient, ip, mac string) *GarbageCollector {
	return &GarbageCollector{
		data:       new(GarbageCollectorData),
		uapiClient: uapiClient,

		ip:  ip,
		mac: mac,
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

func (gc *GarbageCollector) Count() (int, int, int) {
	return len(gc.data.Pods), len(gc.data.Pool), len(gc.data.Unused)
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

	data := &GarbageCollectorData{
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
		gc.kubeClient, err = getNodeKubeClient()
		if err != nil {
			return nil, fmt.Errorf("failed to init node kube client: %v", err)
		}
	}

	opts := metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", gc.ip).String(),
		ResourceVersion: "0",
	}

	podList, err := gc.kubeClient.CoreV1().Pods(corev1.NamespaceAll).List(context.Background(), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	ips := make([]*PodIP, 0, len(podList.Items))
	ipSet := make(map[string]struct{}, len(podList.Items))
	for _, pod := range podList.Items {
		if pod.Spec.HostNetwork {
			continue
		}
		podIPs := pod.Status.PodIPs
		for _, podIP := range podIPs {
			if _, ok := ipSet[podIP.IP]; ok {
				continue
			}
			ipSet[podIP.IP] = struct{}{}

			ips = append(ips, &PodIP{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				IP:        podIP.IP,
			})
		}
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

func (gc *GarbageCollector) Sweep() (int, error) {
	if !gc.force && gc.data.DisableSweep {
		return 0, nil
	}
	if len(gc.data.Unused) == 0 {
		return 0, nil
	}

	vpcClient, err := gc.uapiClient.VPCClient()
	if err != nil {
		return 0, err
	}

	var deleted int
	var live []*UnusedIP
	for _, unused := range gc.data.Unused {
		if deleted >= maxSweep || unused.Lifetime >= 0 {
			live = append(live, unused)
			continue
		}
		deleted++

		req := vpcClient.NewDeleteSecondaryIpRequest()
		req.VPCId = ucloud.String(gc.uapiClient.VPCID())
		req.SubnetId = ucloud.String(gc.uapiClient.SubnetID())
		req.Ip = ucloud.String(unused.IP)
		req.Mac = ucloud.String(gc.mac)

		_, err = vpcClient.DeleteSecondaryIp(req)
		if err != nil {
			// Do not abort the sweep process if releasing the IP fails.
			// These IPs will be rediscovered in the next collect process.
			log.Warningf("failed to release ip %s when running gc, error: %v", unused.IP, err)
			continue
		}
		log.Infof("gc release unused ip %s done", unused.IP)
	}

	gc.data.Unused = live
	err = gc.saveData()
	if err != nil {
		return 0, err
	}

	return deleted, nil
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

func getNodeKubeClient() (*kubernetes.Clientset, error) {
	cfg, err := clientcmd.BuildConfigFromFlags("", nodeKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read kube config: %v", err)
	}
	return kubernetes.NewForConfig(cfg)
}
