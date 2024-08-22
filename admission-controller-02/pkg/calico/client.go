package calico

import (
	calicoClient "github.com/projectcalico/calico/tree/master/libcalico-go/lib/clientv3/"
	"k8s.io/client-go/rest"
)

func NewClient(config *rest.Config) (calicoClient.Interface, error) {
	return calicoClient.NewFromConfig(config)
}
