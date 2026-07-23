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
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/ucloud/uk8s-cni-vpc/rpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7312", "IPAMD gRPC TCP address")
	expected := flag.String("expected", "", "expected CNI version")
	flag.Parse()

	if err := queryVersion(*addr, *expected); err != nil {
		fmt.Fprintf(os.Stderr, "error: %+v\n", err)
		os.Exit(1)
	}
}

func queryVersion(addr, expected string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return errors.Wrapf(err, "main.queryVersion dial %s", addr)
	}
	defer conn.Close()

	client := rpc.NewCNIIpamClient(conn)
	response, err := client.GetCNIVersion(ctx, &rpc.GetCNIVersionRequest{})
	if err != nil {
		return errors.Wrap(err, "main.queryVersion call GetCNIVersion")
	}

	fmt.Printf(
		"version=%s path=%s size=%d mod_time=%s\n",
		response.GetVersion(),
		response.GetPath(),
		response.GetSize(),
		time.Unix(response.GetModTime(), 0).Format(time.RFC3339),
	)

	if expected != "" && response.GetVersion() != expected {
		return errors.Errorf(
			"main.queryVersion got version %s, expected %s",
			errors.Safe(response.GetVersion()),
			errors.Safe(expected),
		)
	}
	return nil
}
