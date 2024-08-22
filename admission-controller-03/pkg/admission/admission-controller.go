package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	calicoClient "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func HandleAdmissionReview(w http.ResponseWriter, r *http.Request) {
	var admissionReviewReq admissionv1.AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&admissionReviewReq); err != nil {
		http.Error(w, fmt.Sprintf("could not decode request: %v", err), http.StatusBadRequest)
		return
	}

	admissionResponse := &admissionv1.AdmissionResponse{
		UID:     admissionReviewReq.Request.UID,
		Allowed: true,
	}

	if admissionReviewReq.Request.Kind.Kind == "Namespace" {
		config, err := clientcmd.BuildConfigFromFlags("", "/path/to/kubeconfig")
		if err != nil {
			http.Error(w, fmt.Sprintf("could not get config: %v", err), http.StatusInternalServerError)
			return
		}

		k8sClient, err := kubernetes.NewForConfig(config)
		if err != nil {
			http.Error(w, fmt.Sprintf("could not create Kubernetes client: %v", err), http.StatusInternalServerError)
			return
		}

		calicoClient, err := calicoClient.NewForConfig(config)
		if err != nil {
			http.Error(w, fmt.Sprintf("could not create Calico client: %v", err), http.StatusInternalServerError)
			return
		}

		if admissionReviewReq.Request.Operation == admissionv1.Create {
			labelSelector := "location=my-location"
			masterPool, err := getMasterPool(calicoClient, labelSelector, "/16")
			if err != nil {
				http.Error(w, fmt.Sprintf("could not find master IP pool: %v", err), http.StatusInternalServerError)
				return
			}

			subnets, err := splitMasterPool(masterPool.Spec.CIDR, "/25")
			if err != nil {
				http.Error(w, fmt.Sprintf("could not split master pool: %v", err), http.StatusInternalServerError)
				return
			}

			availablePool := selectAvailableSubnet(subnets)
			if availablePool == "" {
				http.Error(w, "no available subnets found", http.StatusInternalServerError)
				return
			}

			admissionResponse.Patch = []byte(fmt.Sprintf(`[{"op": "add", "path": "/metadata/annotations/ip-pool", "value": "%s"}]`, availablePool))
			patchType := admissionv1.PatchTypeJSONPatch
			admissionResponse.PatchType = &patchType
		} else if admissionReviewReq.Request.Operation == admissionv1.Delete {
			namespace := admissionReviewReq.Request.Name
			err := markPoolAsAvailable(calicoClient, namespace)
			if err != nil {
				http.Error(w, fmt.Sprintf("could not mark pool as available: %v", err), http.StatusInternalServerError)
				return
			}
		}
	}

	admissionReviewRes := admissionv1.AdmissionReview{
		Response: admissionResponse,
	}
	admissionReviewRes.Response.UID = admissionReviewReq.Request.UID

	if err := json.NewEncoder(w).Encode(admissionReviewRes); err != nil {
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

func getMasterPool(client calicoClient.Interface, labelSelector, cidr string) (*calicoApi.IPPool, error) {
	ipPools, err := client.ProjectcalicoV3().IPPools(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{
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

func splitMasterPool(cidr, newSubnetSize string) ([]string, error) {
	cmd := exec.Command("calicoctl", "ipam", "split", cidr, newSubnetSize)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to split IP pool: %v", err)
	}

	subnets := strings.Split(strings.TrimSpace(string(output)), "\n")
	return subnets, nil
}

func selectAvailableSubnet(subnets []string) string {
	if len(subnets) > 0 {
		return subnets[0]
	}
	return ""
}

func markPoolAsAvailable(client calicoClient.Interface, namespace string) error {
	ipPools, err := client.ProjectcalicoV3().IPPools(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{
		LabelSelector: fmt.Sprintf("namespace=%s", namespace),
	})
	if err != nil {
		return fmt.Errorf("could not fetch IP pool for namespace: %v", err)
	}

	for _, pool := range ipPools.Items {
		patch := []byte(`[{"op": "remove", "path": "/metadata/annotations/ip-pool"}]`)
		_, err = client.ProjectcalicoV3().IPPools(metav1.NamespaceAll).Patch(context.Background(), pool.Name, metav1.PatchTypeJSONPatch, patch, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("could not remove annotation from IP pool: %v", err)
		}

		// Logic to mark the pool as available
		// You may need to update or set the status of the pool here
	}

	return nil
}
