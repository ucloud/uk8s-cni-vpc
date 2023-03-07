package common

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

type PodSecondaryIP struct {
	Name         string   `json:"name"`
	SecondaryIPs []string `json:"secondary_ips"`
}

func ListPodSecondaryIPs() ([]*PodSecondaryIP, error) {
	podList, err := ListPods()
	if err != nil {
		return nil, err
	}

	nodeIP := Node().Name

	ips := make([]*PodSecondaryIP, 0, len(podList.Items))
	for _, pod := range podList.Items {
		if pod.Spec.HostNetwork {
			continue
		}
		podIPs := pod.Status.PodIPs
		item := &PodSecondaryIP{
			Name:         pod.Name,
			SecondaryIPs: make([]string, 0, len(podIPs)),
		}
		for _, podIP := range podIPs {
			if podIP.IP == nodeIP {
				continue
			}
			item.SecondaryIPs = append(item.SecondaryIPs, podIP.IP)
		}
		ips = append(ips, item)
	}

	return ips, nil
}

func ListPods() (*corev1.PodList, error) {
	opts := metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", Node().Name).String(),
		ResourceVersion: "0",
	}

	pods, err := KubeClient().CoreV1().Pods(corev1.NamespaceAll).List(context.Background(), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	return pods, nil
}
