package common

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

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
