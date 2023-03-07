package common

import (
	"context"
	"fmt"
	"os"

	"github.com/ucloud/uk8s-cni-vpc/pkg/ipamd"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
	"google.golang.org/grpc"
)

func IpamdEnable() bool {
	_, err := os.Stat(ipamd.SocketPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		OnError(fmt.Errorf("stat ipamd socket: %v", err))
	}

	return true
}

func IpamdClient() (rpc.CNIIpamClient, error) {
	conn, err := grpc.Dial(ipamd.SocketTarget, grpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	c := rpc.NewCNIIpamClient(conn)
	_, err = c.Ping(context.Background(), &rpc.PingRequest{})
	if err != nil {
		return nil, fmt.Errorf("failed to ping ipamd: %v", err)
	}
	return c, nil
}
