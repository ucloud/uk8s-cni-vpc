package common

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/ucloud/uk8s-cni-vpc/pkg/uapi"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func OnError(err error) {
	fmt.Printf("%s: %v\n", color.RedString("error"), err)
	os.Exit(1)
}

var (
	client *kubernetes.Clientset

	initKubeOnce sync.Once
)

const (
	etcConfigPath     = "/etc/kubernetes/kubelet.kubeconfig"
	etcEnvPath        = "/etc/kubernetes/ucloud"
	nodeInterfaceName = "eth0"
)

func KubeClient() *kubernetes.Clientset {
	var err error
	initKubeOnce.Do(func() {
		err = initKubeConfig()
	})
	if err != nil {
		OnError(err)
	}

	return client
}

func initKubeConfig() error {
	homePath, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home dir: %v", err)
	}

	kubeConfigDir := filepath.Join(homePath, ".kube")
	stat, err := os.Stat(kubeConfigDir)
	switch {
	case err == nil:
		if !stat.IsDir() {
			return fmt.Errorf("%s is not a directory", kubeConfigDir)
		}

	case os.IsNotExist(err):
		err = os.MkdirAll(kubeConfigDir, os.ModePerm)
		if err != nil {
			return fmt.Errorf("failed to ensure config dir: %v", err)
		}

	default:
		return err
	}

	kubeConfigPath := filepath.Join(kubeConfigDir, "config")
	_, err = os.Stat(kubeConfigPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		_, err = os.Stat(etcConfigPath)
		if err != nil {
			return fmt.Errorf("failed to read etc kubeconfig: %v", err)
		}

		err = copyFile(kubeConfigPath, etcConfigPath)
		if err != nil {
			return fmt.Errorf("failed to copy etc kubeconfig: %v", err)
		}
	}

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read kubeconfig: %v", err)
	}

	client, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to init kube client: %v", err)
	}

	return nil
}

func copyFile(dstPath, srcPath string) error {
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer dst.Close()

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	_, err = io.Copy(dst, src)
	return err
}

type NodeInfo struct {
	Name        string
	MAC         string
	Region      string
	APIEndpoint string
	ProjectID   string
	VPCID       string
	SubnetID    string
	IpamdEnable bool
}

var (
	nodeInfo         *NodeInfo
	initNodeInfoOnce sync.Once
)

func Node() *NodeInfo {
	var err error
	initNodeInfoOnce.Do(func() {
		nodeInfo, err = buildInfo()
	})
	if err != nil {
		OnError(err)
	}
	return nodeInfo
}

func buildInfo() (*NodeInfo, error) {
	name, mac, err := getNodeName()
	if err != nil {
		return nil, fmt.Errorf("failed to get node name: %v", err)
	}

	err = parseEnvFile(etcEnvPath)
	if err != nil {
		return nil, fmt.Errorf("faield to parse env file: %v", err)
	}

	region, err := getLocalEnv("UCLOUD_REGION_ID")
	if err != nil {
		return nil, err
	}

	apiEndpoint, err := getLocalEnv("UCLOUD_API_ENDPOINT")
	if err != nil {
		return nil, err
	}

	projectID, err := getLocalEnv("UCLOUD_PROJECT_ID")
	if err != nil {
		return nil, err
	}

	vpcID, err := getLocalEnv("UCLOUD_VPC_ID")
	if err != nil {
		return nil, err
	}

	subnetID, err := getLocalEnv("UCLOUD_SUBNET_ID")
	if err != nil {
		return nil, err
	}

	return &NodeInfo{
		Name:        name,
		MAC:         mac,
		Region:      region,
		APIEndpoint: apiEndpoint,
		ProjectID:   projectID,
		VPCID:       vpcID,
		SubnetID:    subnetID,
		IpamdEnable: IpamdEnable(),
	}, nil
}

func getNodeName() (string, string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", "", fmt.Errorf("failed to get net interfaces: %v", err)
	}
	for _, iface := range ifaces {
		if iface.Name == nodeInterfaceName {
			addrs, err := iface.Addrs()
			if err != nil {
				return "", "", fmt.Errorf("failed to get addrs for iface: %v", err)
			}
			if len(addrs) != 1 {
				return "", "", fmt.Errorf("invalid iface addr count, expect 1, found %d", len(addrs))
			}
			return addrs[0].String(), iface.HardwareAddr.String(), nil
		}
	}
	return "", "", fmt.Errorf("cannot find network interface %s", nodeInterfaceName)
}

func parseEnvFile(path string) error {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		tmp := strings.Split(line, "=")
		if len(tmp) == 2 {
			key := tmp[0]
			val := tmp[1]
			err = os.Setenv(key, val)
			if err != nil {
				return fmt.Errorf("failed to setenv: %v", err)
			}
		}
	}

	return nil
}

func getLocalEnv(key string) (string, error) {
	val := os.Getenv(key)
	if val == "" {
		return "", fmt.Errorf("cannot get env %q", key)
	}
	return val, nil
}

var (
	apiClient   *uapi.ApiClient
	initAPIOnce sync.Once
)

func UAPI() *uapi.ApiClient {
	_ = Node()
	var err error
	initAPIOnce.Do(func() {
		apiClient, err = uapi.NewClient()
	})
	if err != nil {
		OnError(err)
	}
	return apiClient
}
