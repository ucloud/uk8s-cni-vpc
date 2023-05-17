package uapi

import (
	"sync"

	"github.com/ucloud/ucloud-sdk-go/ucloud/metadata"
	"github.com/ucloud/uk8s-cni-vpc/pkg/ulog"
)

var (
	meta     *metadata.Metadata
	metaOnce sync.Once
)

func GetMeta() (*metadata.Metadata, error) {
	var err error
	metaOnce.Do(func() {
		client := metadata.NewClient()
		var md metadata.Metadata
		md, err = client.GetInstanceIdentityDocument()
		if err != nil {
			ulog.Errorf("Get instance metadata error: %v", err)
			return
		}
		meta = &md
	})
	return meta, err
}
