package admission

import (
	"encoding/json"
	"fmt"
	"net/http"

	"admission-controller-02/pkg/calico"
	"admission-controller-02/pkg/utils"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

		calicoClient, err := calico.NewClient(config)
		if err != nil {
			http.Error(w, fmt.Sprintf("could not create Calico client: %v", err), http.StatusInternalServerError)
			return
		}

		if admissionReviewReq.Request.Operation == admissionv1.Create {
			labelSelector := "location=my-location"
			masterPool, err := calico.GetMasterPool(calicoClient, labelSelector, "/16")
			if err != nil {
				http.Error(w, fmt.Sprintf("could not find master IP pool: %v", err), http.StatusInternalServerError)
				return
			}

			subnets, err := calico.SplitMasterPool(masterPool.Spec.CIDR, "/26")
			if err != nil {
				http.Error(w, fmt.Sprintf("could not split master pool: %v", err), http.StatusInternalServerError)
				return
			}

			availablePool := utils.SelectAvailableSubnet(subnets)
			if availablePool == "" {
				http.Error(w, "no available subnets found", http.StatusInternalServerError)
				return
			}

			admissionResponse.Patch = []byte(fmt.Sprintf(`[{"op": "add", "path": "/metadata/annotations/ip-pool", "value": "%s"}]`, availablePool))
			patchType := admissionv1.PatchTypeJSONPatch
			admissionResponse.PatchType = &patchType
		} else if admissionReviewReq.Request.Operation == admissionv1.Delete {
			namespace := admissionReviewReq.Request.Name
			err := calico.MarkPoolAsAvailable(calicoClient, namespace)
			if err != nil {
				http.Error(w, fmt.Sprintf("could not mark pool as available: %v", err), http.StatusInternalServerError)
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
