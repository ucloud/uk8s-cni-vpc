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
	"os"
	"path"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/gammazero/workerpool"
	crdclientset "github.com/ucloud/uk8s-cni-vpc/kubernetes/generated/clientset/versioned"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
)

func kubeConfig() (*rest.Config, error) {
	configPath := os.Getenv("KUBECONFIG")
	if configPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("Get home dir error: %v", err)
		}
		configPath = path.Join(homeDir, ".kube", "config")
		_, err = os.Stat(configPath)
		if err != nil {
			configPath = "/etc/kubernetes/kubelet.kubeconfig"
		}
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		return nil, fmt.Errorf("Read kube config error: %v", err)
	}
	return cfg, nil
}

func kubeClient() (*kubernetes.Clientset, error) {
	cfg, err := kubeConfig()
	if err != nil {
		return nil, err
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Create kube client error: %v", err)
	}
	return client, nil
}

func crdClient() (*crdclientset.Clientset, error) {
	cfg, err := kubeConfig()
	if err != nil {
		return nil, err
	}

	client, err := crdclientset.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("Create crd client error: %v", err)
	}

	return client, nil
}

type Node struct {
	Name   string `json:"name" yaml:"name"`
	Addr   string `json:"addr" yaml:"addr"`
	Subnet string `json:"subnet" yaml:"subnet"`
	Pool   int    `json:"pool" yaml:"pool"`

	conn *grpc.ClientConn
}

func (n *Node) Titles() []string {
	return []string{"NODE", "SUBNET", "POOL"}
}

func (n *Node) Row() []string {
	return []string{
		n.Name,
		n.Subnet,
		strconv.Itoa(n.Pool),
	}
}

func (n *Node) GetID() string   { return n.Name }
func (n *Node) GetNode() string { return "" }

func (n *Node) Dial() (rpc.CNIIpamClient, error) {
	conn, err := grpc.Dial(n.Addr, grpc.WithInsecure(), grpc.WithTimeout(time.Second))
	if err != nil {
		return nil, fmt.Errorf("Dial ipamd %q: %v", n.Addr, err)
	}
	n.conn = conn

	client := rpc.NewCNIIpamClient(conn)
	if !enabledIpamd(client) {
		return nil, fmt.Errorf("Ipamd %q is not enabled", n.Name)
	}

	return client, nil
}

func (n *Node) Close() error {
	if n.conn == nil {
		return nil
	}
	return n.conn.Close()
}

// Check if there is ipamd service available by a gRPC Ping probe.
func enabledIpamd(c rpc.CNIIpamClient) bool {
	_, err := c.Ping(context.Background(), &rpc.PingRequest{})
	if err != nil {
		return false
	}
	return true
}

func ListNodes() ([]*Node, error) {
	client, err := crdClient()
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubeClient()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	ipamdList, err := client.IpamdV1beta1().Ipamds("kube-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("List ipamds error: %v", err)
	}

	nodes := make([]*Node, 0, len(ipamdList.Items))
	for _, ipamd := range ipamdList.Items {
		node := &Node{
			Name:   ipamd.Spec.Node,
			Addr:   ipamd.Spec.Addr,
			Subnet: ipamd.Spec.Subnet,
			Pool:   ipamd.Status.Current,
		}
		_, err = kubeClient.CoreV1().Nodes().Get(ctx, ipamd.GetName(), metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("Get node error: %v", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

func GetNode(name string) (*Node, error) {
	ctx := context.Background()

	kubeClient, err := kubeClient()
	if err != nil {
		return nil, err
	}
	_, err = kubeClient.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf("Could not find node %q", name)
		}
		return nil, fmt.Errorf("Get node error: %v", err)
	}

	client, err := crdClient()
	if err != nil {
		return nil, err
	}
	ipamd, err := client.IpamdV1beta1().Ipamds("kube-system").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Get ipamd %q: %v", name, err)
	}

	return &Node{
		Name:   ipamd.Spec.Node,
		Addr:   ipamd.Spec.Addr,
		Subnet: ipamd.Spec.Subnet,
		Pool:   ipamd.Status.Current,
	}, nil

}

type PoolRecord struct {
	IP   string `json:"ip" yaml:"ip"`
	Node string `json:"node" yaml:"node"`

	CreateTime int64 `json:"create_time" yaml:"create_time"`

	Recycled     bool  `json:"recycled" yaml:"recycled"`
	RecycledTime int64 `json:"recycled_time" yaml:"recycled_time"`

	CheckHealthTime int64 `json:"check_health_time" yaml:"check_health_time"`

	Cooldown bool `json:"cooldown" yaml:"cooldown"`
}

func (r *PoolRecord) Titles() []string {
	return []string{"IP", "RECYCLED", "COOLDOWN", "HEALTH_AGE", "AGE"}
}

func (r *PoolRecord) Row() []string {
	recycled := "<none>"
	if r.Recycled {
		recycled = getAge(r.RecycledTime)
	}
	return []string{
		r.IP,
		recycled,
		fmt.Sprint(r.Cooldown),
		getAge(r.CheckHealthTime),
		getAge(r.CreateTime),
	}
}

func (r *PoolRecord) GetID() string   { return r.IP }
func (r *PoolRecord) GetNode() string { return r.Node }

func ListPool(nodes []*Node) ([]*PoolRecord, error) {
	ctx := context.Background()
	var records []*PoolRecord
	wp := NewWorkerpool(20)
	for _, node := range nodes {
		node := node
		wp.Run(func() error {
			client, err := node.Dial()
			if err != nil {
				return err
			}
			defer node.Close()

			resp, err := client.DescribePool(ctx, &rpc.DescribePoolRequest{})
			if err != nil {
				return fmt.Errorf("grpc Describe pool error: %v", err)
			}

			wp.SingleFlight(func() {
				for _, ip := range resp.Pool {
					records = append(records, &PoolRecord{
						IP:              ip.VPCIP,
						Node:            node.Name,
						CreateTime:      ip.CreateTime,
						Recycled:        ip.Recycled,
						RecycledTime:    ip.RecycleTime,
						CheckHealthTime: ip.CheckHealthTime,
						Cooldown:        false,
					})
				}
				for _, ip := range resp.Cooldown {
					records = append(records, &PoolRecord{
						IP:              ip.VPCIP,
						Node:            node.Name,
						CreateTime:      ip.CreateTime,
						Recycled:        ip.Recycled,
						RecycledTime:    ip.RecycleTime,
						CheckHealthTime: ip.CheckHealthTime,
						Cooldown:        true,
					})
				}
			})

			return nil
		})
	}
	if err := wp.Wait(); err != nil {
		return nil, err
	}

	return records, nil
}

type PodRecord struct {
	Node         string `json:"node" yaml:"node"`
	PodNS        string `json:"pod_ns" yaml:"pod_ns"`
	PodName      string `json:"pod_name" yaml:"pod_name"`
	PodUID       string `json:"pod_uid" yaml:"pod_uid"`
	SandboxID    string `json:"sandbox_id" yaml:"sandbox_id"`
	NetNS        string `json:"net_ns" yaml:"net_ns"`
	VPCIP        string `json:"vpcip" yaml:"vpcip"`
	VPCID        string `json:"vpcid" yaml:"vpcid"`
	SubnetID     string `json:"subnet_id" yaml:"subnet_id"`
	Gateway      string `json:"gateway" yaml:"gateway"`
	Mask         string `json:"mask" yaml:"mask"`
	MacAddress   string `json:"mac_address" yaml:"mac_address"`
	DedicatedUNI bool   `json:"dedicated_uni" yaml:"dedicated_uni"`
	InterfaceID  string `json:"interface_id" yaml:"interface_id"`
	EIPID        string `json:"eipid" yaml:"eipid"`
	CreateTime   int64  `json:"create_time" yaml:"create_time"`
}

func (r *PodRecord) Titles() []string {
	return []string{"NAMESPACE", "NAME", "IP", "AGE"}
}

func (r *PodRecord) Row() []string {
	return []string{
		r.PodNS,
		r.PodName,
		r.VPCIP,
		getAge(r.CreateTime),
	}
}

func (r *PodRecord) GetID() string   { return r.VPCIP }
func (r *PodRecord) GetNode() string { return r.Node }

func ListPod(nodes []*Node) ([]*PodRecord, error) {
	ctx := context.Background()
	var records []*PodRecord
	wp := NewWorkerpool(20)

	for _, node := range nodes {
		node := node
		wp.Run(func() error {
			client, err := node.Dial()
			if err != nil {
				return err
			}
			defer node.Close()

			resp, err := client.ListPodNetworkRecord(ctx, &rpc.ListPodNetworkRecordRequest{})
			if err != nil {
				return fmt.Errorf("grpc ListPodNetworkRecord error: %v", err)
			}

			wp.SingleFlight(func() {
				for _, network := range resp.Networks {
					records = append(records, &PodRecord{
						PodNS:        network.PodNS,
						PodName:      network.PodName,
						PodUID:       network.PodUID,
						SandboxID:    network.SandboxID,
						NetNS:        network.NetNS,
						VPCIP:        network.VPCIP,
						VPCID:        network.VPCID,
						SubnetID:     network.SubnetID,
						Gateway:      network.Gateway,
						Mask:         network.Mask,
						MacAddress:   network.MacAddress,
						DedicatedUNI: network.DedicatedUNI,
						InterfaceID:  network.InterfaceID,
						EIPID:        network.EIPID,
						CreateTime:   network.CreateTime,
						Node:         node.Name,
					})
				}
			})

			return nil
		})
	}

	if err := wp.Wait(); err != nil {
		return nil, err
	}

	return records, nil
}

type UnuseRecord struct {
	Node     string
	IP       string
	SubnetID string
}

func (r *UnuseRecord) Titles() []string {
	return []string{"IP", "SUBNET"}
}

func (r *UnuseRecord) Row() []string {
	return []string{r.IP, r.SubnetID}
}

func (r *UnuseRecord) GetID() string   { return r.IP }
func (r *UnuseRecord) GetNode() string { return r.Node }

func ListUnuse(nodes []*Node) ([]*UnuseRecord, error) {
	ctx := context.Background()
	var records []*UnuseRecord
	wp := NewWorkerpool(20)
	for _, node := range nodes {
		node := node
		wp.Run(func() error {
			client, err := node.Dial()
			if err != nil {
				return err
			}
			defer node.Close()

			resp, err := client.ListUnuse(ctx, &rpc.ListUnuseRequest{})
			if err != nil {
				return fmt.Errorf("grpc ListUnuse error: %v", err)
			}

			wp.SingleFlight(func() {
				for _, ip := range resp.Unuse {
					records = append(records, &UnuseRecord{
						Node:     node.Name,
						IP:       ip.VPCIP,
						SubnetID: ip.SubnetID,
					})
				}
			})

			return nil
		})
	}

	if err := wp.Wait(); err != nil {
		return nil, err
	}

	return records, nil
}

type Table struct {
	ncol int
	rows [][]string
}

func (t *Table) Add(row []string) {
	if len(row) == 0 {
		panic("Row could not be empty")
	}

	if t.ncol == 0 {
		t.ncol = len(row)
	} else if t.ncol != len(row) {
		panic("Invalid row length")
	}

	t.rows = append(t.rows, row)
}

func (t *Table) Show() {
	pads := make([]int, 0, t.ncol)
	for i := 0; i < t.ncol; i++ {
		maxLen := 0
		for _, row := range t.rows {
			cell := row[i]
			if len(cell) > maxLen {
				maxLen = len(cell)
			}
		}
		pads = append(pads, maxLen+2)
	}

	for _, row := range t.rows {
		for i, cell := range row {
			pad := pads[i]
			fmtStr := "%-" + strconv.Itoa(pad) + "s"
			cell = fmt.Sprintf(fmtStr, cell)
			fmt.Print(cell)
		}
		fmt.Println()
	}
}

func getTimeFormat(unix int64) string {
	t := time.Unix(unix, 0)
	return t.Local().Format("2006-01-02 15:04:05")
}

func getAge(unix int64) string {
	before := time.Unix(unix, 0).Local()
	age := time.Since(before)

	years := int(age.Hours()) / 24 / 365
	if years > 1 {
		return fmt.Sprintf("%dy", years)
	}

	days := int(age.Hours()) / 24
	if days > 5 {
		return fmt.Sprintf("%dd", days)
	}

	hours := int(age.Hours())
	if hours > 5 {
		return fmt.Sprintf("%dh", hours)
	}

	minutes := int(age.Minutes())
	if minutes > 5 {
		return fmt.Sprintf("%dm", minutes)
	}

	return fmt.Sprintf("%ds", int(age.Seconds()))
}

func Confirm(msg string, args ...any) {
	msg = fmt.Sprintf(msg, args...)
	fmt.Printf("%s? (y/n) ", msg)
	var confirm string
	fmt.Scanf("%s", &confirm)
	if confirm != "y" {
		os.Exit(1)
	}
}

type Workerpool struct {
	pool *workerpool.WorkerPool
	lock sync.Mutex
	err  error
}

func NewWorkerpool(n int) *Workerpool {
	return &Workerpool{
		pool: workerpool.New(n),
	}
}

func (wp *Workerpool) Run(action func() error) {
	wp.pool.Submit(func() {
		wp.lock.Lock()
		if wp.err != nil {
			wp.lock.Unlock()
			return
		}
		wp.lock.Unlock()

		err := action()
		if err != nil {
			wp.lock.Lock()
			wp.err = err
			wp.lock.Unlock()
		}
	})
}

func (wp *Workerpool) SingleFlight(action func()) {
	wp.lock.Lock()
	defer wp.lock.Unlock()
	action()
}

func (wp *Workerpool) Wait() error {
	wp.pool.StopWait()
	return wp.err
}
