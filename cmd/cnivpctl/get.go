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
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"github.com/ucloud/ucloud-sdk-go/ucloud"
	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
	"github.com/ucloud/uk8s-cni-vpc/pkg/iputils"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"
)

const (
	IpamdServiceSocket = "unix:" + ipamd.IpamdServiceSocket

	storageFile = "/opt/cni/networkbolt.db"
)

var (
	getOutput string
)

var getCmd = &cobra.Command{
	Use:   "get <TYPE> [IP]",
	Short: "Get or list IP info",

	Args: cobra.MaximumNArgs(2),

	RunE: func(_ *cobra.Command, args []string) error {
		resourceType := ""
		if len(args) >= 1 {
			resourceType = args[0]
		}
		filterIP := ""
		if len(args) >= 2 {
			filterIP = args[1]
		}

		var records []Record
		switch resourceType {
		case "":
			ips, err := getSecondaryIP()
			if err != nil {
				return err
			}
			records = make([]Record, len(ips))
			for i, ip := range ips {
				records[i] = ip
			}

		case "unused":
			ips, err := getSecondaryIP()
			if err != nil {
				return err
			}
			for _, ip := range ips {
				if ip.Unused {
					records = append(records, ip)
				}
			}

		case "pod", "po":
			pods, err := getPodNetwork()
			if err != nil {
				return err
			}
			records = make([]Record, len(pods))
			for i, pod := range pods {
				records[i] = pod
			}

		case "pool":
			pool, err := getPool()
			if err != nil {
				return err
			}
			records = make([]Record, len(pool))
			for i, ip := range pool {
				records[i] = ip
			}

		case "cooldown", "cd":
			pool, err := getPool()
			if err != nil {
				return err
			}
			for _, ip := range pool {
				if ip.Cooldown {
					records = append(records, ip)
				}
			}

		case "recycle":
			pool, err := getPool()
			if err != nil {
				return err
			}
			for _, ip := range pool {
				if ip.Recycled {
					records = append(records, ip)
				}
			}

		default:
			return fmt.Errorf("Unknown resource type %s", resourceType)
		}
		if len(records) == 0 {
			fmt.Println("Empty set")
			return nil
		}
		if filterIP != "" {
			filtered := make([]Record, 0, len(records))
			for _, record := range records {
				if record.GetIP() != filterIP {
					continue
				}
				filtered = append(filtered, record)
			}
			records = filtered
		}
		if getOutput == "table" {
			table := &Table{}
			table.Add(records[0].Titles())
			for _, record := range records {
				table.Add(record.Row())
			}
			table.Show()
			return nil
		}

		var value any
		if len(records) == 1 {
			value = records[0]
		} else {
			value = records
		}
		var err error
		switch getOutput {
		case "yaml", "yml":
			err = showYaml(value)

		case "json":
			err = showJson(value)

		default:
			return fmt.Errorf("Unknown output type %s", getOutput)
		}
		if err != nil {
			return fmt.Errorf("Show output error: %v", err)
		}

		return nil
	},
}

func init() {
	getCmd.PersistentFlags().StringVarP(&getOutput, "output", "o", "table", "Output style")
}

type Record interface {
	GetIP() string
	Titles() []string
	Row() []string
}

type SecondaryIP struct {
	IP         string
	CreateTime int64
	Status     string
	Unused     bool
}

func (s *SecondaryIP) GetIP() string {
	return s.IP
}

func (s *SecondaryIP) Titles() []string {
	return []string{"IP", "STATUS"}
}

func (s *SecondaryIP) Row() []string {
	return []string{s.IP, s.Status}
}

func getSecondaryIP() ([]*SecondaryIP, error) {
	podNetworks, err := getPodNetwork()
	if err != nil {
		return nil, err
	}

	pool, err := getPool()
	if err != nil {
		return nil, err
	}

	ipMap := make(map[string]string, len(podNetworks)+len(pool))
	for _, network := range podNetworks {
		ipMap[network.VPCIP] = fmt.Sprintf("pod:%s/%s", network.PodNS, network.PodName)
	}
	for _, ip := range pool {
		status := "pool"
		if ip.Cooldown {
			status = "cooldown"
		}
		ipMap[ip.VPCIP] = status
	}

	ucloudClient, err := uapi.NewClient()
	if err != nil {
		return nil, fmt.Errorf("Create ucloud client error: %v", err)
	}
	vpcClient, err := ucloudClient.VPCClient()
	if err != nil {
		return nil, fmt.Errorf("Create vpc client error: %v", err)
	}

	mac, err := iputils.GetNodeMacAddress("")
	if err != nil {
		return nil, fmt.Errorf("Get mac address error: %v", err)
	}

	req := vpcClient.NewDescribeSecondaryIpRequest()
	req.VPCId = ucloud.String(ucloudClient.VPCID())
	req.SubnetId = ucloud.String(ucloudClient.SubnetID())
	req.Mac = ucloud.String(mac)

	resp, err := vpcClient.DescribeSecondaryIp(req)
	if err != nil {
		return nil, fmt.Errorf("Call UCloud API error: %v", err)
	}

	ips := make([]*SecondaryIP, len(resp.DataSet))
	for i, ipInfo := range resp.DataSet {
		ip := ipInfo.Ip
		if status, ok := ipMap[ip]; ok {
			ips[i] = &SecondaryIP{
				IP:     ip,
				Status: status,
			}
		} else {
			ips[i] = &SecondaryIP{
				IP:     ip,
				Status: "<unused>",
				Unused: true,
			}
		}
	}

	return ips, nil
}

type PodNetwork struct {
	PodNS        string
	PodName      string
	PodUID       string
	SandboxID    string
	NetNS        string
	VPCIP        string
	VPCID        string
	SubnetID     string
	Gateway      string
	Mask         string
	MacAddress   string
	DedicatedUNI bool
	InterfaceID  string
	EIPID        string
	CreateTime   int64
	RecycleTime  int64
	Recycled     bool
}

func (p *PodNetwork) GetIP() string {
	return p.VPCIP
}

func (p *PodNetwork) Titles() []string {
	return []string{"NAMESPACE", "NAME", "IP"}
}

func (p *PodNetwork) Row() []string {
	return []string{p.PodNS, p.PodName, p.VPCIP}
}

func convertPodNetwork(networks []*rpc.PodNetwork) []*PodNetwork {
	records := make([]*PodNetwork, len(networks))
	for i, network := range networks {
		records[i] = &PodNetwork{
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
			RecycleTime:  network.RecycleTime,
			Recycled:     network.Recycled,
		}
	}
	return records
}

func getPodNetwork() ([]*PodNetwork, error) {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err == nil {
		defer conn.Close()
		client := rpc.NewCNIIpamClient(conn)
		if enabledIpamd(client) {
			ctx := context.Background()
			resp, err := client.ListPodNetworkRecord(ctx, &rpc.ListPodNetworkRecordRequest{})
			if err != nil {
				return nil, fmt.Errorf("Call ipamd error: %v", err)
			}
			return convertPodNetwork(resp.Networks), nil
		}
	}

	handler, err := database.BoltHandler(storageFile)
	if err != nil {
		return nil, fmt.Errorf("Create boltdb handler error: %v", err)
	}

	db, err := database.NewBolt[rpc.PodNetwork](ipamd.NetworkDBName, handler)
	if err != nil {
		return nil, fmt.Errorf("Create boltdb error: %v", err)
	}

	kvs, err := db.List()
	if err != nil {
		return nil, fmt.Errorf("List pod network from boltdb error: %v", err)
	}

	networks := database.Values(kvs)
	return convertPodNetwork(networks), nil
}

type PoolRecord struct {
	VPCIP       string
	CreateTime  int64
	RecycleTime int64
	Recycled    bool
	Cooldown    bool
}

func (p *PoolRecord) GetIP() string {
	return p.VPCIP
}

func (p *PoolRecord) Titles() []string {
	return []string{
		"IP",
		"CREATE_TIME",
		"RECYCLED",
		"COOLDOWN",
	}
}

func (p *PoolRecord) Row() []string {
	createTime := "<none>"
	if p.CreateTime > 0 {
		createTime = getTimeFormat(p.CreateTime)
	}

	recycle := "<none>"
	if p.Recycled && p.RecycleTime > 0 {
		recycle = getTimeFormat(p.RecycleTime)
	}

	cooldown := fmt.Sprintf("%v", p.Cooldown)
	return []string{p.VPCIP, createTime, recycle, cooldown}
}

func convertPoolRecord(pool []*rpc.PoolRecord) []*PoolRecord {
	records := make([]*PoolRecord, len(pool))
	for i, pool := range pool {
		records[i] = &PoolRecord{
			VPCIP:       pool.VPCIP,
			CreateTime:  pool.CreateTime,
			RecycleTime: pool.RecycleTime,
			Recycled:    pool.Recycled,
			Cooldown:    pool.Cooldown,
		}
	}
	return records
}

func getPool() ([]*PoolRecord, error) {
	conn, err := grpc.Dial(IpamdServiceSocket, grpc.WithInsecure())
	if err == nil {
		defer conn.Close()
		client := rpc.NewCNIIpamClient(conn)
		if enabledIpamd(client) {
			ctx := context.Background()
			resp, err := client.ListPoolRecord(ctx, &rpc.ListPoolRecordRequest{})
			if err != nil {
				return nil, fmt.Errorf("Call ipamd error: %v", err)
			}
			return convertPoolRecord(resp.Pool), nil
		}
	}

	handler, err := database.BoltHandler(storageFile)
	if err != nil {
		return nil, fmt.Errorf("Create boltdb handler error: %v", err)
	}

	readPool := func(bucket string, cooldown bool) ([]*rpc.PoolRecord, error) {
		db, err := database.NewBolt[rpc.PodNetwork](bucket, handler)
		if err != nil {
			return nil, fmt.Errorf("Create boltdb for %s error: %v", bucket, err)
		}

		kvs, err := db.List()
		if err != nil {
			return nil, fmt.Errorf("List boltdb for %s error: %v", bucket, err)
		}
		networks := database.Values(kvs)
		records := make([]*rpc.PoolRecord, len(networks))
		for i, network := range networks {
			records[i] = &rpc.PoolRecord{
				VPCIP:       network.VPCIP,
				CreateTime:  network.CreateTime,
				RecycleTime: network.RecycleTime,
				Recycled:    network.Recycled,
				Cooldown:    cooldown,
			}
		}

		return records, nil
	}

	pool, err := readPool(ipamd.PoolDBName, false)
	if err != nil {
		return nil, err
	}

	cooldown, err := readPool(ipamd.CooldownDBName, true)
	if err != nil {
		return nil, err
	}

	pool = append(pool, cooldown...)
	return convertPoolRecord(pool), nil
}

func showYaml(value any) error {
	encoder := yaml.NewEncoder(os.Stdout)
	encoder.SetIndent(2)
	err := encoder.Encode(value)
	if err != nil {
		return fmt.Errorf("Encode yaml error: %v", err)
	}

	return nil
}

func showJson(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	err := encoder.Encode(value)
	if err != nil {
		return fmt.Errorf("Encode json error: %v", err)
	}

	return nil
}

// Check if there is ipamd service available by a gRPC Ping probe.
func enabledIpamd(c rpc.CNIIpamClient) bool {
	_, err := c.Ping(context.Background(), &rpc.PingRequest{})
	if err != nil {
		return false
	}
	return true
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
