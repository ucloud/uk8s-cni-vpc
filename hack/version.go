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
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/cockroachdb/errors"
)

type versionResponse struct {
	Version string `json:"version"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7313", "IPAMD local version HTTP address")
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

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/version", nil)
	if err != nil {
		return errors.Wrap(err, "main.queryVersion create request")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return errors.Wrapf(err, "main.queryVersion GET http://%s/version", addr)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.Errorf("main.queryVersion unexpected HTTP status %s", errors.Safe(response.Status))
	}

	var result versionResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return errors.Wrap(err, "main.queryVersion decode response")
	}
	if result.Version == "" {
		return errors.New("main.queryVersion response has an empty version")
	}

	fmt.Printf("version=%s\n", result.Version)

	if expected != "" && result.Version != expected {
		return errors.Errorf(
			"main.queryVersion got version %s, expected %s",
			errors.Safe(result.Version),
			errors.Safe(expected),
		)
	}
	return nil
}
