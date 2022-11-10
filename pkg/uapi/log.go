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
		logrus.Errorf(fmt, args)
	} else {
		klog.Errorf(fmt, args)
	}
}

func logWarningf(fmt string, args ...interface{}) {
	if getMyExecName() == binCNI {
		logrus.Warningf(fmt, args)
	} else {
		klog.Warningf(fmt, args)
	}
}

func logInfof(fmt string, args ...interface{}) {
	if getMyExecName() == binCNI {
		logrus.Infof(fmt, args)
	} else {
		klog.Infof(fmt, args)
	}
}
