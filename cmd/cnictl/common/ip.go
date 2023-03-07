package common

import (
	"context"
	"fmt"
	"sort"

	"github.com/ucloud/uk8s-cni-vpc/rpc"
)

type IPSummary struct {
	Allocated  []string          `json:"-"`
	Pods       []*PodSecondaryIP `json:"pods,omitempty"`
	PodIPCount int               `json:"-"`
	Pool       []string          `json:"pool,omitempty"`
	Unused     []string          `json:"unused,omitempty"`
}

func SummarizeIP() (*IPSummary, error) {
	allocated, err := ListSecondaryIP()
	if err != nil {
		return nil, err
	}
	allocatedIPs := make([]string, len(allocated))
	for i, ip := range allocated {
		allocatedIPs[i] = ip.IP
	}

	pods, err := ListPodSecondaryIPs()
	if err != nil {
		return nil, err
	}

	node := Node()
	var pool []string
	if node.IpamdEnable {
		ipamdClient, err := IpamdClient()
		if err != nil {
			return nil, fmt.Errorf("failed to init ipamd client: %v", err)
		}
		resp, err := ipamdClient.Status(context.Background(), &rpc.StatusRequest{})
		if err != nil {
			return nil, fmt.Errorf("failed to request ipamd status: %v", err)
		}
		pool = resp.Pool
	}

	unused := make(map[string]struct{}, len(allocatedIPs))
	var podIPCount int
	for _, ip := range allocatedIPs {
		unused[ip] = struct{}{}
	}
	for _, pod := range pods {
		podIPCount += len(pod.SecondaryIPs)
		for _, ip := range pod.SecondaryIPs {
			delete(unused, ip)
		}
	}
	for _, ip := range pool {
		delete(unused, ip)
	}

	unusedIPs := make([]string, 0, len(unused))
	for ip := range unused {
		unusedIPs = append(unusedIPs, ip)
	}
	sort.Strings(unusedIPs)

	return &IPSummary{
		Allocated:  allocatedIPs,
		Pods:       pods,
		PodIPCount: podIPCount,
		Pool:       pool,
		Unused:     unusedIPs,
	}, nil
}
