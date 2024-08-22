package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"

	calicoApi "github.com/projectcalico/api/pkg/apis/v3"
	calicoClient "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func handleAdmissionReview(w http.ResponseWriter, r *http.Request) {
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
		config, err := rest.InClusterConfig()
		if err != nil {
			http.Error(w, fmt.Sprintf("could not get in-cluster config: %v", err), http.StatusInternalServerError)
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
			// Step 1: Fetch the master IP pool
			masterPool, err := getMasterPool(calicoClient, "location=my-location", "/16")
			if err != nil {
				http.Error(w, fmt.Sprintf("could not find master IP pool: %v", err), http.StatusInternalServerError)
				return
			}

			// Step 2: Split the master pool into /25 subnets
			subnets, err := splitMasterPool(masterPool.Spec.CIDR, "/25")
			if err != nil {
				http.Error(w, fmt.Sprintf("could not split master pool: %v", err), http.StatusInternalServerError)
				return
			}

			// Step 3: Select an available subnet
			availablePool := selectAvailableSubnet(subnets)
			if availablePool == "" {
				http.Error(w, "no available subnets found", http.StatusInternalServerError)
				return
			}

			// Step 4: Patch the namespace with the selected IP pool
			admissionResponse.Patch = []byte(fmt.Sprintf(`[{"op": "add", "path": "/metadata/annotations/ip-pool", "value": "%s"}]`, availablePool))
			patchType := admissionv1.PatchTypeJSONPatch
			admissionResponse.PatchType = &patchType
		} else if admissionReviewReq.Request.Operation == admissionv1.Delete {
			// Step 5: Handle namespace deletion - remove annotation
			namespace := admissionReviewReq.Request.Name
			err := removePoolAnnotation(calicoClient, namespace)
			if err != nil {
				http.Error(w, fmt.Sprintf("could not remove annotation from pool: %v", err), http.StatusInternalServerError)
				return
			}
		}
	}

	admissionReviewRes := admissionv1.AdmissionReview{
		Response: admissionResponse,
	}

	if err := json.NewEncoder(w).Encode(admissionReviewRes); err != nil {
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// Fetch the master pool based on label and subnet size
func getMasterPool(client calicoClient.Interface, labelSelector, cidr string) (*calicoApi.IPPool, error) {
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

// Split the master pool into smaller subnets using calicoctl
func splitMasterPool(cidr, newSubnetSize string) ([]string, error) {
	cmd := exec.Command("calicoctl", "ipam", "split", cidr, newSubnetSize)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to split IP pool: %v", err)
	}

	subnets := strings.Split(strings.TrimSpace(string(output)), "\n")
	return subnets, nil
}

// Select the first available subnet
func selectAvailableSubnet(subnets []string) string {
	// Here you would check which subnets are in use and return the first available one
	// For simplicity, this example assumes all subnets are available
	if len(subnets) > 0 {
		return subnets[0]
	}
	return ""
}

// Remove annotation from IP pool when namespace is deleted
func removePoolAnnotation(client calicoClient.Interface, namespace string) error {
	// Fetch the pool associated with the namespace
	poolName := getPoolNameFromNamespace(namespace) // Implement this function as needed
	pool, err := client.IPPools().Get(context.Background(), poolName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not fetch IP pool: %v", err)
	}

	// Remove the annotation marking it as used
	patch := []byte(`[{"op": "remove", "path": "/metadata/annotations/ip-pool"}]`)
	_, err = client.IPPools().Patch(context.Background(), poolName, metav1.PatchTypeJSONPatch, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("could not remove annotation from IP pool: %v", err)
	}

	// Logic to mark the pool as available
	// ...

	return nil
}

// Helper function to extract pool name from namespace
func getPoolNameFromNamespace(namespace string) string {
	// Implement your logic to retrieve the associated pool name from the namespace
	return "example-pool-name" // Placeholder
}

func main() {
	http.HandleFunc("/mutate", handleAdmissionReview)
	server := &http.Server{
		Addr: ":8443",
	}
	fmt.Println("Starting webhook server on port 8443...")
	if err := server.ListenAndServeTLS("/tls/tls.crt", "/tls/tls.key"); err != nil {
		panic(err.Error())
	}
}
