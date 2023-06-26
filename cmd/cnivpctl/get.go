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
	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
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

	Args: cobra.RangeArgs(1, 2),

	RunE: func(_ *cobra.Command, args []string) error {
		resourceType := args[0]
		filterIP := ""
		if len(args) >= 2 {
			filterIP = args[1]
		}

		var value any
		switch resourceType {
		case "pod", "po":
			networks, err := getPodNetwork()
			if err != nil {
				return err
			}
			if filterIP != "" {
				filtered := make([]*rpc.PodNetwork, 0, len(networks))
				for _, network := range networks {
					if network.VPCIP != filterIP {
						continue
					}
					filtered = append(filtered, network)
				}
				networks = filtered
			}
			if len(networks) == 0 {
				fmt.Println("Could not find pod record in current node")
				return nil
			}
			if getOutput == "table" {
				table := &Table{}
				table.Add([]string{
					"NAMESPACE",
					"NAME",
					"IP",
				})
				for _, network := range networks {
					row := []string{
						network.PodNS,
						network.PodName,
						network.VPCIP,
					}
					table.Add(row)
				}
				table.Show()
				return nil
			}
			if len(networks) == 1 {
				value = networks[0]
			} else {
				value = networks
			}

		case "pool":
			pool, err := getPool()
			if err != nil {
				return err
			}
			if filterIP != "" {
				filtered := make([]*rpc.PoolRecord, 0, len(pool))
				for _, ip := range pool {
					if ip.VPCIP != filterIP {
						continue
					}
					filtered = append(filtered, ip)
				}
				pool = filtered
			}
			if len(pool) == 0 {
				fmt.Println("Could not find pool record in current node")
				return nil
			}
			if getOutput == "table" {
				table := &Table{}
				table.Add([]string{
					"IP",
					"CREATE_TIME",
					"RECYCLED",
					"COOLDOWN",
				})
				for _, ip := range pool {
					createTime := "<none>"
					if ip.CreateTime > 0 {
						createTime = getTimeFormat(ip.CreateTime)
					}

					recycle := "<none>"
					if ip.Recycled && ip.RecycleTime > 0 {
						recycle = getTimeFormat(ip.RecycleTime)
					}

					cooldown := fmt.Sprintf("%v", ip.Cooldown)

					row := []string{ip.VPCIP, createTime, recycle, cooldown}
					table.Add(row)
				}

				table.Show()
				return nil
			}
			if len(pool) == 1 {
				value = pool[0]
			} else {
				value = pool
			}

		default:
			return fmt.Errorf("Unknown resource type %s", resourceType)
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

func getPodNetwork() ([]*rpc.PodNetwork, error) {
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
			return resp.Networks, nil
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

	return database.Values(kvs), nil
}

func getPool() ([]*rpc.PoolRecord, error) {
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
			return resp.Pool, nil
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

	return append(pool, cooldown...), nil
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
