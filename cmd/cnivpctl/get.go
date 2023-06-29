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
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

type Record interface {
	Titles() []string
	TitlesWide() []string

	Row() []string
	RowWide() []string

	ID() string
}

func convertToRecords(slice any) []Record {
	if slice == nil {
		return nil
	}
	value := reflect.ValueOf(slice)
	if value.IsNil() || value.Kind() != reflect.Slice {
		return nil
	}
	size := value.Len()
	records := make([]Record, size)
	for i := 0; i < size; i++ {
		ele := value.Index(i)
		record := ele.Interface().(Record)
		records[i] = record
	}
	return records
}

var (
	getOutput string
	getNode   string
)

func init() {
	getCmd.PersistentFlags().StringVarP(&getOutput, "output", "o", "", "Output type")
	getCmd.PersistentFlags().StringVarP(&getNode, "node", "n", "", "Node")
}

var getCmd = &cobra.Command{
	Use:   "get <RESOURCE> [ID]",
	Short: "Get resource",

	Args: cobra.RangeArgs(1, 2),

	RunE: func(_ *cobra.Command, args []string) error {
		nodes, err := ListNodes()
		if err != nil {
			return err
		}
		if getNode != "" {
			var found *Node
			for _, node := range nodes {
				if node.Name == getNode {
					found = node
					break
				}
			}
			if found == nil {
				return fmt.Errorf("Could not find node %q", getNode)
			}
			nodes = []*Node{found}
		}

		resourceType := args[0]
		var records []Record
		switch resourceType {
		case "node", "no", "nodes":
			records = convertToRecords(nodes)

		case "pod", "po", "pods":
			pods, err := ListPod(nodes)
			if err != nil {
				return err
			}
			records = convertToRecords(pods)

		case "pool":
			pool, err := ListPool(nodes)
			if err != nil {
				return err
			}
			records = convertToRecords(pool)

		default:
			return fmt.Errorf("Unknown resource type %q", resourceType)
		}

		if len(args) >= 2 {
			id := args[1]
			newRecords := make([]Record, 0, 1)
			for _, record := range records {
				if strings.Contains(record.ID(), id) {
					newRecords = append(newRecords, record)
				}
			}
			records = newRecords
		}

		if len(records) == 0 {
			fmt.Println("No record")
			return nil
		}

		if getOutput == "" || getOutput == "wide" {
			table := &Table{}
			titles := records[0].Titles()
			if getOutput == "wide" {
				titles = append(titles, records[0].TitlesWide()...)
			}
			table.Add(titles)

			for _, record := range records {
				row := record.Row()
				if getOutput == "wide" {
					row = append(row, record.RowWide()...)
				}
				table.Add(row)
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

		switch getOutput {
		case "yaml", "yml":
			err = showYaml(value)

		case "json":
			err = showJson(value)

		default:
			return fmt.Errorf("Unknown output %q", getOutput)
		}

		return err
	},
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
