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
	"time"

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
	if need, cfg := s.podNeedDedicatedUNI(p); need {
		uni, err := s.setupDedicatedUNIForPod(p, netNS, cfg)
		if err != nil {
			ulog.Errorf("Setup dedicated UNI for pod %s/%s error: %v", req.GetPodName(), req.GetPodNamespace(), err)
			return &rpc.AddPodNetworkResponse{
				Code: rpc.CNIErrorCode_CNIAllocateUNIFailure,
			}, status.Error(codes.Internal, err.Error())
		} else {
			pNet := &rpc.PodNetwork{
				PodName:      podName,
				PodNS:        podNS,
				PodUID:       string(p.UID),
				SandboxID:    sandboxID,
				NetNS:        netNS,
				VPCIP:        uni.PrivateIpSet[0],
				VPCID:        uni.VPCId,
				SubnetID:     uni.SubnetId,
				Gateway:      uni.Gateway,
				Mask:         uni.Netmask,
				MacAddress:   uni.MacAddress,
				DedicatedUNI: true,
				InterfaceID:  uni.InterfaceId,
				CreateTime:   time.Now().Unix(),
			}
			if len(uni.EIPIdSet) > 0 {
				pNet.EIPID = uni.EIPIdSet[0]
			}
			return &rpc.AddPodNetworkResponse{
				Code:       rpc.CNIErrorCode_CNISuccess,
				PodNetwork: pNet,
			}, nil
		}
	} else {
		// Send InnerAddPodNetworkRequest to ipPoolWatermarkManager by golang channel
		recv := make(chan *InnerAddPodNetworkResponse, 0)
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
			s.backupReleaseSecondaryIP(resp.PodNetwork.VPCIP)
			ulog.Errorf("SetAnnotationForCalicoPolicy %s, %s, %s error: %v",
				req.GetPodName(), req.GetPodNamespace(), req.GetSandboxID(), resp.Err)
			return &rpc.AddPodNetworkResponse{
				Code: rpc.CNIErrorCode_CNIAllocateSecondaryIPFailure,
			}, status.Error(codes.Internal, err.Error())
		}

		pNet := resp.PodNetwork
		pNet.PodName = podName
		pNet.PodNS = podNS
		pNet.SandboxID = sandboxID
		pNet.PodUID = string(p.UID)
		pNet.NetNS = netNS
		return &rpc.AddPodNetworkResponse{
			Code:       rpc.CNIErrorCode_CNISuccess,
			PodNetwork: pNet,
		}, nil
	}
}

func (s *ipamServer) DelPodNetwork(ctx context.Context, req *rpc.DelPodNetworkRequest) (*rpc.DelPodNetworkResponse, error) {
	ulog.Infof("grpc: begin to recycle ip %s", req.GetPodNetwork().GetVPCIP())
	pNet := req.GetPodNetwork()
	if pNet == nil {
		return &rpc.DelPodNetworkResponse{
			Code: rpc.CNIErrorCode_CNISuccess,
		}, nil
	}

	if pNet.DedicatedUNI {
		err := s.tearDownDedicatedUNIForPod(pNet)
		if err != nil {
			return &rpc.DelPodNetworkResponse{
				Code: rpc.CNIErrorCode_CNIReleaseUNIFailure,
			}, status.Error(codes.InvalidArgument, fmt.Sprintf("Cannot release UNI: %v", err))
		} else {
			return &rpc.DelPodNetworkResponse{
				Code: rpc.CNIErrorCode_CNISuccess,
			}, nil
		}
	}

	// Send InnerDelPodNetworkRequest to ipPoolWatermarkManager by golang channel
	recv := make(chan error, 0)
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
			pNet.PodNS, pNet.PodName, pNet.SandboxID, err)
		return &rpc.DelPodNetworkResponse{
			Code: rpc.CNIErrorCode_CNIReleaseSecondaryIPFailure,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to delete secondary ip, %v", err))
	}
}

func (s *ipamServer) AddPodNetworkRecord(ctx context.Context, req *rpc.AddPodNetworkRecordRequest) (*rpc.AddPodNetworkRecordResponse, error) {
	pNet := req.GetPodNetwork()
	err := s.networkDB.Put(database.PodKey(pNet.PodName, pNet.PodNS, pNet.SandboxID), pNet)
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
	if req.MacAddr == "" {
		return &rpc.BorrowIPResponse{
			Code: rpc.CNIErrorCode_CNIMissingParameters,
		}, status.Error(codes.InvalidArgument, "missing MacAddr")
	}
	ip, err := s.lendIP(req.MacAddr)
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

func (s *ipamServer) DescribePool(ctx context.Context, req *rpc.DescribePoolRequest) (*rpc.DescribePoolResponse, error) {
	poolKvs, err := s.poolDB.List()
	if err != nil {
		return &rpc.DescribePoolResponse{
			Code: rpc.CNIErrorCode_CNIReadDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to read pool from db: %v", err))
	}

	cooldownKvs, err := s.cooldownDB.List()
	if err != nil {
		return &rpc.DescribePoolResponse{
			Code: rpc.CNIErrorCode_CNIReadDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to read cooldown from db: %v", err))
	}
	cooldown := make([]*rpc.PodNetwork, len(cooldownKvs))
	for i, kv := range cooldownKvs {
		cooldown[i] = kv.Value.Network
	}

	return &rpc.DescribePoolResponse{
		Code:     rpc.CNIErrorCode_CNISuccess,
		Pool:     database.Values(poolKvs),
		Cooldown: cooldown,
	}, nil
}

func (s *ipamServer) PushPool(ctx context.Context, req *rpc.PushPoolRequest) (*rpc.PushPoolResponse, error) {
	info, err := s.uapiEnsureSecondaryIP(req.IP)
	if err != nil {
		return &rpc.PushPoolResponse{
			Code: rpc.CNIErrorCode_CNIK8SAPIError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to ensure ip: %v", err))
	}

	network := convertIpToPodNetwork(info)
	if !s.putIpToPool(network) {
		return &rpc.PushPoolResponse{
			Code: rpc.CNIErrorCode_CNIWriteDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to save ip to db"))
	}

	return &rpc.PushPoolResponse{
		Code: rpc.CNIErrorCode_CNISuccess,
		IP:   network,
	}, nil
}

func (s *ipamServer) PopPool(ctx context.Context, req *rpc.PopPoolRequest) (*rpc.PopPoolResponse, error) {
	var network *rpc.PodNetwork
	var err error
	if req.IP != "" {
		vpcID := s.uapi.VPCID()
		key := vpcID + "-" + req.IP
		network, err = s.poolDB.Get(key)
		if err == nil {
			err = s.poolDB.Delete(key)
		}
	} else {
		var kv *database.KeyValue[rpc.PodNetwork]
		kv, err = s.poolDB.Pop()
		if kv != nil {
			network = kv.Value
		}
	}
	if err != nil {
		return &rpc.PopPoolResponse{
			Code: rpc.CNIErrorCode_CNIReadDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to read pool db: %v", err))
	}

	err = s.uapiDeleteSecondaryIp(network.VPCIP)
	if err != nil {
		s.backupPushSecondaryIP(network)
		return &rpc.PopPoolResponse{
			Code: rpc.CNIErrorCode_CNIReleaseSecondaryIPFailure,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to delete ip: %v", err))
	}

	return &rpc.PopPoolResponse{
		Code: rpc.CNIErrorCode_CNISuccess,
		IP:   network,
	}, nil
}

func (s *ipamServer) ListUnuse(ctx context.Context, req *rpc.ListUnuseRequest) (*rpc.ListUnuseResponse, error) {
	ips, err := s.uapiListSecondaryIP()
	if err != nil {
		return &rpc.ListUnuseResponse{
			Code: rpc.CNIErrorCode_CNIK8SAPIError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to list ip: %v", err))
	}

	used, err := s.usedIP()
	if err != nil {
		return &rpc.ListUnuseResponse{
			Code: rpc.CNIErrorCode_CNIReadDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to read ip: %v", err))
	}

	unuse := make([]*rpc.PodNetwork, 0)
	for _, ip := range ips {
		if _, ok := used[ip.Ip]; ok {
			continue
		}
		unuse = append(unuse, convertIpToPodNetwork(ip))
	}

	return &rpc.ListUnuseResponse{
		Code:  rpc.CNIErrorCode_CNISuccess,
		Unuse: unuse,
	}, nil
}

func (s *ipamServer) ReleaseIP(ctx context.Context, req *rpc.ReleaseIPRequest) (*rpc.ReleaseIPResponse, error) {
	used, err := s.usedIP()
	if err != nil {
		return &rpc.ReleaseIPResponse{
			Code: rpc.CNIErrorCode_CNIReadDBError,
		}, status.Error(codes.Internal, fmt.Sprintf("failed to read ip: %v", err))
	}

	for _, ip := range req.IP {
		if _, ok := used[ip]; ok {
			return &rpc.ReleaseIPResponse{
				Code: rpc.CNIErrorCode_CNIReleaseUNIFailure,
			}, status.Error(codes.Internal, fmt.Sprintf("ip %s is still in used, can not be released", ip))
		}
	}

	for _, ip := range req.IP {
		err = s.uapiDeleteSecondaryIp(ip)
		if err != nil {
			return &rpc.ReleaseIPResponse{
				Code: rpc.CNIErrorCode_CNIReleaseUNIFailure,
			}, status.Error(codes.Internal, fmt.Sprintf("failed to release %s: %v", ip, err))
		}
	}

	return &rpc.ReleaseIPResponse{
		Code: rpc.CNIErrorCode_CNISuccess,
	}, nil
}
