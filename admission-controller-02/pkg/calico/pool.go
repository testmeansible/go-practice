package calico

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	calicoApi "github.com/projectcalico/calico/tree/master/libcalico-go/lib/apis/v3"
	calicoClient "github.com/projectcalico/calico/tree/master/libcalico-go/lib/clientv3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GetMasterPool(client calicoClient.Interface, labelSelector, cidr string) (*calicoApi.IPPool, error) {
	ipPools, err := client.IPPools().List(context.Background(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("could not list IP pools: %v", err)
	}

	for _, pool := range ipPools.Items {
		if pool.Spec.CIDR == cidr {
			return &pool, nil
		}
	}
	return nil, fmt.Errorf("no matching IP pool found")
}

func SplitMasterPool(cidr, newSubnetSize string) ([]string, error) {
	cmd := exec.Command("calicoctl", "ipam", "split", cidr, newSubnetSize)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to split IP pool: %v", err)
	}

	subnets := strings.Split(strings.TrimSpace(string(output)), "\n")
	return subnets, nil
}

func MarkPoolAsAvailable(client calicoClient.Interface, namespace string) error {
	ipPool, err := client.IPPools().Get(context.Background(), namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not fetch IP pool for namespace: %v", err)
	}

	patch := []byte(`[{"op": "remove", "path": "/metadata/annotations/ip-pool"}]`)
	_, err = client.IPPools().Patch(context.Background(), ipPool.Name, metav1.PatchTypeJSONPatch, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("could not remove annotation from IP pool: %v", err)
	}

	return nil
}
