package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	// crdv1 "github.com/projectcalico/api/pkg/apis/crd.projectcalico.org/v1"
	crdv1 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	"github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type AdmissionController struct {
	Clientset    *clientset.Clientset
	K8sClientset *kubernetes.Clientset
}

func NewAdmissionController() (*AdmissionController, error) {
	// config, err := rest.InClusterConfig()
	// if err != nil {
	// 	return nil, fmt.Errorf("could not get in-cluster config: %v", err)
	// }

	config, err := clientcmd.BuildConfigFromFlags("", "C:\\Users\\Mansoor\\Desktop\\kube\\config")
	if err != nil {
		panic(err.Error())
	}

	clientset, err := clientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("could not create Calico clientset: %v", err)
	}

	k8sClientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("could not create Kubernetes clientset: %v", err)
	}

	return &AdmissionController{
		Clientset:    clientset,
		K8sClientset: k8sClientset,
	}, nil
}

// Implement your logic for handling admission requests
func (a *AdmissionController) HandleAdmissionReview(w http.ResponseWriter, r *http.Request) {
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
		if admissionReviewReq.Request.Operation == admissionv1.Create {
			// Handle namespace creation logic here

			// Fetch the available IP pools
			ipPools, err := a.Clientset.ProjectcalicoV3().IPPools().List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				admissionResponse.Allowed = false
				admissionResponse.Result = &metav1.Status{
					Message: fmt.Sprintf("could not list IP pools: %v", err),
				}
				writeAdmissionResponse(w, admissionResponse)
				return
			}

			// Select an available subnet
			availableSubnet := a.selectAvailableSubnet(ipPools.Items)
			if availableSubnet == "" {
				admissionResponse.Allowed = false
				admissionResponse.Result = &metav1.Status{
					Message: "No available subnets found.",
				}
				writeAdmissionResponse(w, admissionResponse)
				return
			}

			// Step 3: Select an available subnet
			// availablePool := a.selectAvailableSubnet()
			// if availablePool == "" {
			// 	admissionResponse.Allowed = false
			// 	admissionResponse.Result = &metav1.Status{
			// 		Message: "No available subnets found.",
			// 	}
			// 	writeAdmissionResponse(w, admissionResponse)
			// 	return
			// }

			// Step 4: Patch the namespace with the selected IP pool
			patch := []map[string]interface{}{
				{
					"op":    "add",
					"path":  "/metadata/annotations/ip-pool",
					"value": availableSubnet,
				},
			}

			patchBytes, err := json.Marshal(patch)
			if err != nil {
				http.Error(w, fmt.Sprintf("could not marshal patch: %v", err), http.StatusInternalServerError)
				return
			}

			admissionResponse.Patch = patchBytes
			admissionResponse.PatchType = func() *admissionv1.PatchType {
				pt := admissionv1.PatchTypeJSONPatch
				return &pt
			}()

			// Update the IP pool label to "used"
			if err := a.updateIPPoolLabel(availableSubnet, "used"); err != nil {
				admissionResponse.Allowed = false
				admissionResponse.Result = &metav1.Status{
					Message: fmt.Sprintf("could not update IP pool label: %v", err),
				}
				writeAdmissionResponse(w, admissionResponse)
				return
			}

		} else if admissionReviewReq.Request.Operation == admissionv1.Delete {
			// Handle namespace deletion logic here

			// Step 1: Fetch the namespace to get the IP pool annotation
			namespace := admissionReviewReq.Request.Name
			ns, err := a.K8sClientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
			if err != nil {
				http.Error(w, fmt.Sprintf("could not fetch namespace: %v", err), http.StatusInternalServerError)
				return
			}

			ipPool := ns.Annotations["ip-pool"]
			if ipPool == "" {
				writeAdmissionResponse(w, admissionResponse)
				return
			}

			// Step 2: Mark the IP pool as available by removing the annotation
			patch := []map[string]interface{}{
				{
					"op":   "remove",
					"path": "/metadata/annotations/ip-pool",
				},
			}

			patchBytes, err := json.Marshal(patch)
			if err != nil {
				http.Error(w, fmt.Sprintf("could not marshal patch: %v", err), http.StatusInternalServerError)
				return
			}

			admissionResponse.Patch = patchBytes
			admissionResponse.PatchType = func() *admissionv1.PatchType {
				pt := admissionv1.PatchTypeJSONPatch
				return &pt
			}()
		}
	}

	writeAdmissionResponse(w, admissionResponse)
}

// Select an available subnet
func (a *AdmissionController) selectAvailableSubnet(subnets []crdv1.IPPool) string {
	for _, subnet := range subnets {
		labels := subnet.ObjectMeta.Labels
		if location, ok := labels["location"]; ok && location == "zone-lhr" {
			if status, ok := labels["status"]; ok && status == "available" {
				return subnet.Name
			}
		}
	}
	return ""
}

func (a *AdmissionController) updateIPPoolLabel(poolName, newStatus string) error {
	ipPool, err := a.Clientset.ProjectcalicoV3().IPPools().Get(context.TODO(), poolName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("could not get IP pool: %v", err)
	}

	labels := ipPool.ObjectMeta.Labels
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["status"] = newStatus
	ipPool.ObjectMeta.Labels = labels

	_, err = a.Clientset.ProjectcalicoV3().IPPools().Update(context.TODO(), ipPool, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("could not update IP pool: %v", err)
	}
	return nil
}

func writeAdmissionResponse(w http.ResponseWriter, admissionResponse *admissionv1.AdmissionResponse) {
	admissionReview := admissionv1.AdmissionReview{
		Response: admissionResponse,
	}

	if err := json.NewEncoder(w).Encode(admissionReview); err != nil {
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
}
