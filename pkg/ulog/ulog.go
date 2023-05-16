package ulog

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
	"k8s.io/klog/v2"
)

var service bool = true

func BinaryMode(logFilePath string) {
	service = false
	if logFilePath != "" {
		logrus.SetOutput(&lumberjack.Logger{
			Filename:   logFilePath,
			MaxSize:    50, // Megabytes
			MaxBackups: 3,
			MaxAge:     10,   // Days
			Compress:   true, // Disabled by default
		})
	}
}

func Infof(format string, args ...any) {
	if service {
		klog.InfoDepth(1, fmt.Sprintf(format, args...))
	} else {
		logrus.WithField("call", getCaller()).Infof(format, args...)
	}
}

func Errorf(format string, args ...any) {
	if service {
		klog.ErrorDepth(1, fmt.Sprintf(format, args...))
	} else {
		logrus.WithField("call", getCaller()).Errorf(format, args...)
	}
}

func Warnf(format string, args ...any) {
	if service {
		klog.WarningDepth(1, fmt.Sprintf(format, args...))
	} else {
		logrus.WithField("call", getCaller()).Warnf(format, args...)
	}
}

func Fatalf(format string, args ...any) {
	if service {
		klog.FatalDepth(1, fmt.Sprintf(format, args...))
	} else {
		logrus.WithField("call", getCaller()).Fatalf(format, args...)
	}
}

func Flush() {
	if service {
		klog.Flush()
	}
}

func getCaller() string {
	pc, _, _, ok := runtime.Caller(2)
	if ok {
		fn := runtime.FuncForPC(pc)
		file, line := fn.FileLine(pc)
		name := filepath.Base(file)
		return fmt.Sprintf("%s:%d", name, line)
	}
	return ""
}
