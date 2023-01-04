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

package uapi

import (
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"k8s.io/klog/v2"
)

const (
	binCNI = "cnivpc"
)

func getMyExecName() string {
	return filepath.Base(os.Args[0])
}

func logErrorf(fmt string, args ...interface{}) {
	if getMyExecName() == binCNI {
		logrus.Errorf(fmt, args...)
	} else {
		klog.Errorf(fmt, args...)
	}
}

func logWarningf(fmt string, args ...interface{}) {
	if getMyExecName() == binCNI {
		logrus.Warningf(fmt, args...)
	} else {
		klog.Warningf(fmt, args...)
	}
}

func logInfof(fmt string, args ...interface{}) {
	if getMyExecName() == binCNI {
		logrus.Infof(fmt, args...)
	} else {
		klog.Infof(fmt, args...)
	}
}
