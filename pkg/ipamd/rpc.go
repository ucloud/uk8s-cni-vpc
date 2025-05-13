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

	"github.com/ucloud/uk8s-cni-vpc/pkg/database"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
	"github.com/ucloud/uk8s-cni-vpc/rpc"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *ipamServer) Ping(ctx context.Context, req *rpc.PingRequest) (*rpc.PingResponse, error) {
	return &rpc.PingResponse{}, nil
}

func (s *ipamServer) AddPodNetwork(ctx context.Context, req *rpc.AddPodNetworkRequest) (*rpc.AddPodNetworkResponse, error) {
	podName := req.GetPodName()
	podNS := req.GetPodNamespace()
	netNS := req.GetNetns()
	sandboxID := req.GetSandboxID()
	ulog.Infof("Begin to assign IP for pod %s/%s", podNS, podName)
	p, err := s.getPod(podName, podNS)
	if err != nil {
		ulog.Errorf("Get pod %s/%s error: %v", podName, podNS, err)
		return &rpc.AddPodNetworkResponse{
			Code: rpc.CNIErrorCode_CNIK8SAPIError,
		}, status.Error(codes.Internal, err.Error())
	}

	// Send InnerAddPodNetworkRequest to ipPoolWatermarkManager by golang channel
	recv := make(chan *InnerAddPodNetworkResponse)
	chanAddPodIP <- &InnerAddPodNetworkRequest{
		Req:      req,
		Receiver: recv,
	}
	resp := <-recv
	if resp.Err != nil {
		ulog.Errorf("Allocate pod ip for %s, %s, %s error: %v",
			req.GetPodName(), req.GetPodNamespace(), req.GetSandboxID(), resp.Err)
		return &rpc.AddPodNetworkResponse{
			Code: rpc.CNIErrorCode_CNIAllocateSecondaryIPFailure,
		}, status.Error(codes.Internal, resp.Err.Error())
	}
	//获取到该信息之后同时把ip信息写入到annotation
	err = s.setAnnotationForCalicoPolicy(p, resp.PodNetwork)
	if err != nil {
		// 写入annotation失败要回收IP，不然会导致IP泄漏
		s.backupReleaseSecondaryIP(resp.PodNetwork)
		ulog.Errorf("SetAnnotationForCalicoPolicy %s, %s, %s error: %v",
			req.GetPodName(), req.GetPodNamespace(), req.GetSandboxID(), resp.Err)
		return &rpc.AddPodNetworkResponse{
			Code: rpc.CNIErrorCode_CNIAllocateSecondaryIPFailure,
		}, status.Error(codes.Internal, err.Error())
	}

	pn := resp.PodNetwork
	pn.PodName = podName
	pn.PodNS = podNS
	pn.SandboxID = sandboxID
	pn.PodUID = string(p.UID)
	pn.NetNS = netNS
	return &rpc.AddPodNetworkResponse{
		Code:       rpc.CNIErrorCode_CNISuccess,
		PodNetwork: pn,
	}, nil
}

func (s *ipamServer) DelPodNetwork(ctx context.Context, req *rpc.DelPodNetworkRequest) (*rpc.DelPodNetworkResponse, error) {
	ulog.Infof("grpc: begin to recycle ip %s", req.GetPodNetwork().GetVPCIP())
	pn := req.GetPodNetwork()
	if pn == nil {
		return &rpc.DelPodNetworkResponse{
			Code: rpc.CNIErrorCode_CNISuccess,
		}, nil
	}

	// Send InnerDelPodNetworkRequest to ipPoolWatermarkManager by golang channel
	recv := make(chan error)
	chanDelPodIP <- &InnerDelPodNetworkRequest{
		Req:      req,
		Receiver: recv,
	}
	err := <-recv
	if err == nil {
		return &rpc.DelPodNetworkResponse{
			Code: rpc.CNIErrorCode_CNISuccess,
		}, nil
	} else {
		ulog.Errorf("Release pod ip for %s/%s, %s error: %v",
			pn.PodNS, pn.PodName, pn.SandboxID, err)
		return &rpc.DelPodNetworkResponse{
			Code: rpc.CNIErrorCode_CNIReleaseSecondaryIPFailure,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to delete secondary ip, %v", err))
	}
}

func (s *ipamServer) AddPodNetworkRecord(ctx context.Context, req *rpc.AddPodNetworkRecordRequest) (*rpc.AddPodNetworkRecordResponse, error) {
	pn := req.GetPodNetwork()
	err := s.networkDB.Put(database.PodKey(pn.PodName, pn.PodNS, pn.SandboxID), pn)
	if err == nil {
		return &rpc.AddPodNetworkRecordResponse{
			Code: rpc.CNIErrorCode_CNISuccess,
		}, nil
	} else {
		return &rpc.AddPodNetworkRecordResponse{
			Code: rpc.CNIErrorCode_CNIWriteDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to write pod network into db: %v", err))
	}
}

func (s *ipamServer) DelPodNetworkRecord(ctx context.Context, req *rpc.DelPodNetworkRecordRequest) (*rpc.DelPodNetworkRecordResponse, error) {
	err := s.networkDB.Delete(database.PodKey(req.GetPodName(), req.GetPodNS(), req.GetSandboxID()))
	if err == nil {
		return &rpc.DelPodNetworkRecordResponse{
			Code: rpc.CNIErrorCode_CNISuccess,
		}, nil
	} else {
		return &rpc.DelPodNetworkRecordResponse{
			Code: rpc.CNIErrorCode_CNIWriteDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to write pod network into db: %v", err))
	}
}

func (s *ipamServer) GetPodNetworkRecord(ctx context.Context, req *rpc.GetPodNetworkRecordRequest) (*rpc.GetPodNetworkRecordResponse, error) {
	p, err := s.networkDB.Get(database.PodKey(req.GetPodName(), req.GetPodNS(), req.GetSandboxID()))
	if err == nil {
		return &rpc.GetPodNetworkRecordResponse{
			PodNetwork: p,
			Code:       rpc.CNIErrorCode_CNISuccess,
		}, nil
	} else {
		return &rpc.GetPodNetworkRecordResponse{
			Code: rpc.CNIErrorCode_CNIReadDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to read pod network from db: %v", err))
	}
}

func (s *ipamServer) ListPodNetworkRecord(ctx context.Context, req *rpc.ListPodNetworkRecordRequest) (*rpc.ListPodNetworkRecordResponse, error) {
	kvs, err := s.networkDB.List()
	if err != nil {
		return &rpc.ListPodNetworkRecordResponse{
			Code: rpc.CNIErrorCode_CNIReadDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to read pod network from db: %v", err))
	}

	resp := &rpc.ListPodNetworkRecordResponse{
		Code:     rpc.CNIErrorCode_CNISuccess,
		Networks: make([]*rpc.PodNetwork, len(kvs)),
	}
	for i, kv := range kvs {
		network := kv.Value
		resp.Networks[i] = network
	}

	return resp, nil
}

func (s *ipamServer) BorrowIP(ctx context.Context, req *rpc.BorrowIPRequest) (*rpc.BorrowIPResponse, error) {
	if req.MacAddress == "" {
		return &rpc.BorrowIPResponse{
			Code: rpc.CNIErrorCode_CNIMissingParameters,
		}, status.Error(codes.InvalidArgument, "missing MacAddr")
	}
	if req.SubnetID == "" {
		return &rpc.BorrowIPResponse{
			Code: rpc.CNIErrorCode_CNIMissingParameters,
		}, status.Error(codes.InvalidArgument, "missing SubnetID")
	}

	ip, err := s.lendIP(req)
	if err != nil {
		return &rpc.BorrowIPResponse{
			Code: rpc.CNIErrorCode_CNIBorrowIPFailure,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to borrow ip: %v", err))
	}
	return &rpc.BorrowIPResponse{
		Code: rpc.CNIErrorCode_CNISuccess,
		IP:   ip,
	}, nil
}

func (s *ipamServer) SupportMultiSubnet(ctx context.Context, req *rpc.SupportMultiSubnetRequest) (*rpc.SupportMultiSubnetResponse, error) {
	return &rpc.SupportMultiSubnetResponse{
		Code:    rpc.CNIErrorCode_CNISuccess,
		Support: true,
	}, nil
}
