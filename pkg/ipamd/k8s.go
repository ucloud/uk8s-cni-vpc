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
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ucloud/uk8s-cni-vpc/pkg/rpc"

	v1beta1 "github.com/ucloud/uk8s-cni-vpc/pkg/generated/clientset/versioned/typed/vipcontroller/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

const (
	KubeNodeLabelUhostID    = "UhostID"
	KubeNodeZoneKey         = "failure-domain.beta.kubernetes.io/zone"
	KubeNodeZoneTopologyKey = "topology.kubernetes.io/zone"
	KubeNodeInstanceTypeKey = "node.uk8s.ucloud.cn/instance_type"
	KubeNodeMachineTypeKey  = "node.uk8s.ucloud.cn/machine_type"

	AnnotationPodNeedEIP       = "network.kubernetes.io/eip"
	AnnotationEIPID            = "network.kubernetes.io/eip-id"
	AnnotationEIPAddr          = "network.kubernetes.io/eip-addr"
	AnnotationUNIID            = "network.kubernetes.io/uni-id"
	AnnotationEIPPayMode       = "network.kubernetes.io/eip-paymode"
	AnnotationEIPBandwidth     = "network.kubernetes.io/eip-bandwidth"
	AnnotationEIPChargeType    = "network.kubernetes.io/eip-chargetype"
	AnnotationEIPQuantity      = "network.kubernetes.io/eip-quantity"
	AnnotationSecurityGroupId  = "network.kubernetes.io/security-group-id"
	AnnotationShareBandwidthId = "network.kubernetes.io/sharebandwidth-id"

	//解决calico policy 在使用了postStart情况下规则无法及时下发的问题 https://github.com/projectcalico/calico/issues/3499
	//annotaion 见calico项目 https://github.com/projectcalico/calico/blob/79b442a53adb7d7f1fd62927d9322daf87dce9de/libcalico-go/lib/backend/k8s/conversion/constants.go
	//用户想使用该特性，必须要在启动参数设置calicoPolicyFlag=true
	AnnotationCalicoPolicyPodIP = "cni.projectcalico.org/podIP"
)

type EIPCfg struct {
	Bandwidth        int
	Quantity         int
	EIPId            string
	PayMode          string
	ChargeType       string
	SecurityGroupId  string
	ShareBandwidthId string
}

func (s *ipamServer) setPodAnnotation(pod *v1.Pod, pairs map[string]string) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, localErr := s.k8s.CoreV1().Pods(pod.Namespace).Get(context.TODO(), pod.Name, metav1.GetOptions{})
		if localErr != nil {
			return localErr
		}
		annotations := pod.Annotations
		if annotations == nil {
			annotations = make(map[string]string)
		}
		for k, v := range pairs {
			annotations[k] = v
		}
		// This action may overwrite previous pairs
		pod.Annotations = annotations
		_, localErr = s.k8s.CoreV1().Pods(pod.Namespace).Update(context.TODO(), pod, metav1.UpdateOptions{})
		return localErr
	})
	if err != nil {
		klog.Errorf("Cannot set annotation pair %v for pod %s/%s, %v", pairs, pod.Name, pod.Namespace, err)
	}

	return err
}

func (s *ipamServer) getLocalPods() (*v1.PodList, error) {
	options := metav1.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("spec.nodeName", os.Getenv("KUBE_NODE_NAME")).String(),
		ResourceVersion: "0",
	}
	list, err := s.k8s.CoreV1().Pods(v1.NamespaceAll).List(context.TODO(), options)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods on %s from apiserver", s.nodeName)
	}
	return list, nil
}

func (s *ipamServer) getKubeNodeLabel(nodeName, key string) (string, error) {
	node, err := s.getKubeNode(nodeName)
	if err != nil {
		klog.Errorf("Cannot get kube node %v, %v", nodeName, err)
		return "", err
	}

	if val, found := node.ObjectMeta.Labels[key]; found {
		klog.Infof("Get key %v from node labels for node %v, value: %v", key, nodeName, val)
		return val, nil
	}

	klog.Warningf("Cannot find label %v for node %s", key, nodeName)
	return "", fmt.Errorf("cannot find label %v for node %s", key, nodeName)
}

func (s *ipamServer) getKubeNode(nodeName string) (*v1.Node, error) {
	node, err := s.k8s.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return node, nil
}

func (s *ipamServer) getPod(podName, podNS string) (*v1.Pod, error) {
	pod, err := s.k8s.CoreV1().Pods(podNS).Get(context.TODO(), podName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to get pod %s/%s, %v", podName, podNS, err)
		return nil, err
	}
	return pod, nil
}

func (s *ipamServer) podNeedDedicatedUNI(pod *v1.Pod) (bool, *EIPCfg) {
	if val, found := pod.Annotations[AnnotationPodNeedEIP]; found {
		if strings.ToLower(val) == "true" {
			cfg := &EIPCfg{
				PayMode:          pod.Annotations[AnnotationEIPPayMode],
				ChargeType:       pod.Annotations[AnnotationEIPChargeType],
				ShareBandwidthId: pod.Annotations[AnnotationShareBandwidthId],
				SecurityGroupId:  pod.Annotations[AnnotationSecurityGroupId],
				Bandwidth:        2,
				Quantity:         1,
			}
			if len(pod.Annotations[AnnotationEIPBandwidth]) > 0 {
				if bandwidth, err := strconv.Atoi(pod.Annotations[AnnotationEIPBandwidth]); err == nil && bandwidth > 0 {
					cfg.Bandwidth = bandwidth
				} else {
					klog.Warningf("Cannot not parse annotation %s, %v", AnnotationEIPBandwidth, err)
				}
			}
			if len(pod.Annotations[AnnotationEIPQuantity]) > 0 {
				if quantity, err := strconv.Atoi(pod.Annotations[AnnotationEIPQuantity]); err == nil && quantity > 0 {
					cfg.Quantity = quantity
				} else {
					klog.Warningf("Cannot not parse annotation %s, %v", AnnotationEIPQuantity, err)
				}
			}
			if len(pod.Annotations[AnnotationEIPID]) > 0 {
				cfg.EIPId = pod.Annotations[AnnotationEIPID]
			}

			return true, cfg
		}
	}
	return false, nil
}

// 参数calicoPolicyFlag设置为true则在申请到ip以后更新到annotation
func (s *ipamServer) setAnnotationForCalicoPolicy(pod *v1.Pod, network *rpc.PodNetwork) error {
	if !CalicoPolicyFlag {
		return nil
	}
	anno := pod.Annotations
	if anno == nil {
		anno = make(map[string]string)
	}
	anno[AnnotationCalicoPolicyPodIP] = network.VPCIP
	err := s.setPodAnnotation(pod, anno)
	if err != nil {
		return err
	}
	return nil
}

func getK8sClient() *kubernetes.Clientset {
	// Generate in-cluster config from text
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to generate kubernetes client config", err)
	}

	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Cannot setup kube client connection to kube apiserver, %v", err)
	}
	return clientset
}

func getVipClient() *v1beta1.VipcontrollerV1beta1Client {
	// Generate in-cluster config from text
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to generate kubernetes client config", err)
	}

	// Create the clientset
	clientset, err := v1beta1.NewForConfig(config)
	if err != nil {
		klog.Fatalf("Cannot setup kube client connection to kube apiserver, %v", err)
	}
	return clientset
}

func (s *ipamServer) podEnableStaticIP(podName, podNS string) (bool, *v1.Pod, error) {
	statefulset := false
	pod, err := s.getPod(podName, podNS)
	if err != nil {
		klog.Errorf("Get pod failed, err: %v", err)
		return false, nil, err
	}

	for _, of := range pod.OwnerReferences {
		if of.Kind == "StatefulSet" {
			statefulset = true
			break
		}
	}

	if !statefulset {
		return false, pod, nil
	}

	if val, found := pod.Annotations[STS_ENABLE_STATIC_IP]; found {
		if strings.ToLower(val) == "true" {
			return true, pod, nil
		}
	}

	return false, pod, nil
}
