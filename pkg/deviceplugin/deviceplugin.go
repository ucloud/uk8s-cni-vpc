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

package deviceplugin

import (
	"context"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"google.golang.org/grpc"
	"k8s.io/apimachinery/pkg/util/wait"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	ResourceName = "ucloud.cn/uni"
	serverSock   = pluginapi.DevicePluginPath + "%d-" + "uni.sock"
)

var uniServerSockRegex = regexp.MustCompile("^.*" + "-uni.sock")

// UNIDevicePlugin implements the Kubernetes device plugin API
type UNIDevicePlugin struct {
	socket string
	server *grpc.Server
	count  int
	stop   chan struct{}
	sync.Locker
}

// NewUNIDevicePlugin returns an initialized UNIDevicePlugin
func NewUNIDevicePlugin(count int) *UNIDevicePlugin {
	pluginEndpoint := fmt.Sprintf(serverSock, time.Now().Unix())
	ulog.Infof("Start UNI DevicePlugin, UNI capacity is %d", count)
	return &UNIDevicePlugin{
		socket: pluginEndpoint,
		count:  count,
	}
}

// dial establishes the gRPC communication with the registered device plugin.
func dial(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	c, err := grpc.Dial(unixSocketPath, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithTimeout(timeout),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)

	if err != nil {
		return nil, err
	}

	return c, nil
}

// Start starts the gRPC server of the device plugin
func (m *UNIDevicePlugin) Start() error {
	if m.server != nil {
		close(m.stop)
		m.server.Stop()
	}
	err := m.cleanup()
	if err != nil {
		return err
	}

	sock, err := net.Listen("unix", m.socket)
	if err != nil {
		return err
	}

	m.server = grpc.NewServer([]grpc.ServerOption{}...)
	pluginapi.RegisterDevicePluginServer(m.server, m)

	m.stop = make(chan struct{}, 1)
	go m.server.Serve(sock)

	// Wait for server to start by launching a blocking connection
	conn, err := dial(m.socket, 5*time.Second)
	if err != nil {
		return err
	}
	conn.Close()

	return nil
}

// GetDevicePluginOptions return device plugin options
func (m *UNIDevicePlugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{}, nil
}

// PreStartContainer return container prestart hook
func (m *UNIDevicePlugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// Stop stops the gRPC server
func (m *UNIDevicePlugin) Stop() error {
	if m.server == nil {
		return nil
	}

	m.server.Stop()
	m.server = nil
	close(m.stop)

	return m.cleanup()
}

func (m *UNIDevicePlugin) GetPreferredAllocation(ctx context.Context, req *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	return nil, nil
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (m *UNIDevicePlugin) Register(request pluginapi.RegisterRequest) error {
	conn, err := dial(pluginapi.KubeletSocket, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)

	_, err = client.Register(context.Background(), &request)
	if err != nil {
		return err
	}
	return nil
}

// ListAndWatch lists devices and update that list according to the health status
func (m *UNIDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	var devs []*pluginapi.Device
	for i := 0; i < m.count; i++ {
		devs = append(devs, &pluginapi.Device{ID: fmt.Sprintf("uni-%d", i), Health: pluginapi.Healthy})
	}
	s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})
	ticker := time.NewTicker(time.Second * 5)
	for {
		select {
		case <-ticker.C:
			err := s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})
			if err != nil {
				ulog.Errorf("Send device informance error: %v", err)
			}
		case <-m.stop:
			return nil
		}
	}
}

// Allocate which return list of devices.
func (m *UNIDevicePlugin) Allocate(ctx context.Context, r *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	response := pluginapi.AllocateResponse{
		ContainerResponses: []*pluginapi.ContainerAllocateResponse{},
	}

	ulog.Infof("Request Containers: %v", r.GetContainerRequests())
	for range r.GetContainerRequests() {
		response.ContainerResponses = append(response.ContainerResponses,
			&pluginapi.ContainerAllocateResponse{},
		)
	}

	return &response, nil
}

func (m *UNIDevicePlugin) cleanup() error {
	preSocks, err := ioutil.ReadDir(pluginapi.DevicePluginPath)
	if err != nil {
		return err
	}

	for _, preSock := range preSocks {
		if uniServerSockRegex.Match([]byte(preSock.Name())) && preSock.Mode()&os.ModeSocket != 0 {
			if err = syscall.Unlink(path.Join(pluginapi.DevicePluginPath, preSock.Name())); err != nil {
				ulog.Errorf("Clean up previous device plugin listens error: %+v", err)
			}
		}
	}
	return nil
}

func (m *UNIDevicePlugin) watchKubeletRestart() {
	wait.Until(func() {
		_, err := os.Stat(m.socket)
		if err == nil {
			return
		}
		if os.IsNotExist(err) {
			ulog.Infof("device plugin socket %s removed, restarting.", m.socket)
			m.Stop()
			err := m.Start()
			if err != nil {
				ulog.Fatalf("error restart device plugin after kubelet restart %+v", err)
			}
			err = m.Register(
				pluginapi.RegisterRequest{
					Version:      pluginapi.Version,
					Endpoint:     path.Base(m.socket),
					ResourceName: ResourceName,
				},
			)
			if err != nil {
				ulog.Fatalf("error register device plugin after kubelet restart %+v", err)
			}
			return
		}
		ulog.Fatalf("error stat socket: %+v", err)
	}, time.Second*30, make(chan struct{}, 1))
}

// Serve starts the gRPC server and register the device plugin to Kubelet
func (m *UNIDevicePlugin) Serve(resourceName string) error {
	err := m.Start()
	if err != nil {
		ulog.Errorf("Start device plugin error: %v", err)
		return err
	}
	time.Sleep(5 * time.Second)
	ulog.Infof("Starting to serve on %s", m.socket)

	err = m.Register(
		pluginapi.RegisterRequest{
			Version:      pluginapi.Version,
			Endpoint:     path.Base(m.socket),
			ResourceName: resourceName,
		},
	)
	if err != nil {
		ulog.Errorf("Register device plugin error: %v", err)
		m.Stop()
		return err
	}
	ulog.Infof("Registered device plugin with Kubelet")
	go m.watchKubeletRestart()

	return nil
}
