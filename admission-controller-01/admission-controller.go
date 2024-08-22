package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

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

	admissionResponse := admissionv1.AdmissionResponse{
		Allowed: true,
	}

	if admissionReviewReq.Request.Kind.Kind == "Namespace" {
		config, err := rest.InClusterConfig()
		if err != nil {
			panic(err.Error())
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			panic(err.Error())
		}

		// Example: Use clientset to check if the namespace exists
		_, err = clientset.CoreV1().Namespaces().Get(context.TODO(), admissionReviewReq.Request.Name, metav1.GetOptions{})
		if err != nil {
			admissionResponse.Allowed = false
			admissionResponse.Result = &metav1.Status{
				Message: fmt.Sprintf("namespace %s not found: %v", admissionReviewReq.Request.Name, err),
			}
		} else {
			admissionResponse.Patch = []byte(`[{"op": "add", "path": "/metadata/labels/ip-pool", "value": "pool-1"}]`)
			patchType := admissionv1.PatchTypeJSONPatch
			admissionResponse.PatchType = &patchType
		}
	}

	admissionReviewRes := admissionv1.AdmissionReview{
		Response: &admissionResponse,
	}
	admissionReviewRes.Response.UID = admissionReviewReq.Request.UID

	if err := json.NewEncoder(w).Encode(admissionReviewRes); err != nil {
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
		return
	}
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
