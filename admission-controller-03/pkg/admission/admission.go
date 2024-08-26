package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	// crdv1 "github.com/projectcalico/api/pkg/apis/crd.projectcalico.org/v1"
	crdv1 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	"github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type AdmissionController struct {
	Clientset    *clientset.Clientset
	K8sClientset *kubernetes.Clientset
	Logger       *zap.Logger
}

func NewAdmissionController(logger *zap.Logger) (*AdmissionController, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		logger.Error("could not get in-cluster config", zap.Error(err))
		return nil, fmt.Errorf("could not get in-cluster config: %v", err)
	}

	// config, err := clientcmd.BuildConfigFromFlags("", "C:\\Users\\aaaaaa\\Desktop\\kube\\config")
	// if err != nil {
	// 	panic(err.Error())
	// }

	clientset, err := clientset.NewForConfig(config)
	if err != nil {
		logger.Error("could not create Calico clientset", zap.Error(err))
		return nil, fmt.Errorf("could not create Calico clientset: %v", err)
	}

	k8sClientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		logger.Error("could not create Kubernetes clientset", zap.Error(err))
		return nil, fmt.Errorf("could not create Kubernetes clientset: %v", err)
	}

	// logger, _ := zap.NewProduction() // Create a logger
	// defer logger.Sync()              // Flushes buffer, if any

	return &AdmissionController{
		Clientset:    clientset,
		K8sClientset: k8sClientset,
		Logger:       logger,
	}, nil
}

// Implement your logic for handling admission requests
func (a *AdmissionController) HandleAdmissionReview(w http.ResponseWriter, r *http.Request) {
	a.Logger.Info("Handling admission review request")

	var admissionReviewReq admissionv1.AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&admissionReviewReq); err != nil {
		a.Logger.Error("could not decode request", zap.Error(err))
		http.Error(w, fmt.Sprintf("could not decode request: %v", err), http.StatusBadRequest)
		return
	}

	admissionResponse := &admissionv1.AdmissionResponse{
		UID:     admissionReviewReq.Request.UID,
		Allowed: true,
	}

	if admissionReviewReq.Request.Kind.Kind == "Namespace" {
		switch admissionReviewReq.Request.Operation {
		case admissionv1.Create:
			a.handleNamespaceCreation(w, admissionReviewReq, admissionResponse)

		case admissionv1.Delete:
			a.handleNamespaceDeletion(w, admissionReviewReq, admissionResponse)

		case admissionv1.Update:
			// Pass through UPDATE operations
			a.Logger.Info("Passing through update request")
			admissionResponse.Allowed = true
			a.writeAdmissionResponse(w, admissionResponse)

		default:
			a.Logger.Warn("Unsupported operation", zap.String("operation", string(admissionReviewReq.Request.Operation)))
			admissionResponse.Allowed = false
			admissionResponse.Result = &metav1.Status{
				Message: "Unsupported operation",
			}
			a.writeAdmissionResponse(w, admissionResponse)
		}
	} else {
		a.Logger.Warn("Unsupported resource kind", zap.String("kind", admissionReviewReq.Request.Kind.Kind))
		admissionResponse.Allowed = false
		admissionResponse.Result = &metav1.Status{
			Message: "Unsupported resource kind",
		}
		a.writeAdmissionResponse(w, admissionResponse)
	}

	a.Logger.Info("Admission review request handled successfully")
}

func (a *AdmissionController) handleNamespaceCreation(w http.ResponseWriter, admissionReviewReq admissionv1.AdmissionReview, admissionResponse *admissionv1.AdmissionResponse) {
	a.Logger.Info("Processing namespace creation", zap.String("namespace", admissionReviewReq.Request.Name))

	// Fetch available IP pools
	ipPools, err := a.Clientset.ProjectcalicoV3().IPPools().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		a.Logger.Error("could not list IP pools", zap.Error(err))
		admissionResponse.Allowed = false
		admissionResponse.Result = &metav1.Status{
			Message: fmt.Sprintf("could not list IP pools: %v", err),
		}
		a.writeAdmissionResponse(w, admissionResponse)
		return
	}

	// Select an available subnet
	availableSubnet := a.selectAvailableSubnet(ipPools.Items)
	if availableSubnet == "" {
		a.Logger.Warn("No available subnets found")
		admissionResponse.Allowed = false
		admissionResponse.Result = &metav1.Status{
			Message: "No available subnets found.",
		}
		a.writeAdmissionResponse(w, admissionResponse)
		return
	}

	a.Logger.Info("Selected subnet for namespace", zap.String("subnet", availableSubnet))

	// Patch the namespace with the selected IP pool
	annotationValue := fmt.Sprintf(`["%s"]`, availableSubnet)
	patch := []map[string]interface{}{
		{
			"op":    "add",
			"path":  "/metadata/annotations",
			"value": map[string]string{}, // Ensure annotations map exists
		},
		{
			"op":    "add",
			"path":  "/metadata/annotations/cni.projectcalico.org~1ipv4pools", // Escaping "/" character
			"value": annotationValue,
		},
	}

	if err := a.applyPatch(w, admissionReviewReq, patch); err != nil {
		a.Logger.Error("could not apply patch", zap.Error(err))
		admissionResponse.Allowed = false
		admissionResponse.Result = &metav1.Status{
			Message: fmt.Sprintf("could not apply patch: %v", err),
		}
		a.writeAdmissionResponse(w, admissionResponse)
		return
	}

	// Update the IP pool label to "used"
	if err := a.updateIPPoolLabel(availableSubnet, "used"); err != nil {
		a.Logger.Error("could not update IP pool label", zap.Error(err))
		admissionResponse.Allowed = false
		admissionResponse.Result = &metav1.Status{
			Message: fmt.Sprintf("could not update IP pool label: %v", err),
		}
		a.writeAdmissionResponse(w, admissionResponse)
	}
}

func (a *AdmissionController) handleNamespaceDeletion(w http.ResponseWriter, admissionReviewReq admissionv1.AdmissionReview, admissionResponse *admissionv1.AdmissionResponse) {
	namespace := admissionReviewReq.Request.Name
	a.Logger.Info("Handling namespace deletion", zap.String("namespace", namespace))

	// Fetch the namespace to get the IP pool annotation
	ns, err := a.K8sClientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
	if err != nil {
		a.Logger.Error("could not fetch namespace", zap.Error(err))
		http.Error(w, fmt.Sprintf("could not fetch namespace: %v", err), http.StatusInternalServerError)
		return
	}

	// Fetch the annotation value
	ipPoolAnnotation, found := ns.Annotations["cni.projectcalico.org/ipv4pools"]
	if !found || ipPoolAnnotation == "" {
		a.Logger.Warn("No IP pool annotation found, nothing to update")
		a.writeAdmissionResponse(w, admissionResponse)
		return
	}

	// Decode JSON array from annotation
	var ipPools []string
	if err := json.Unmarshal([]byte(ipPoolAnnotation), &ipPools); err != nil {
		a.Logger.Error("Failed to decode IP pool annotation", zap.String("annotation", ipPoolAnnotation), zap.Error(err))
		http.Error(w, fmt.Sprintf("could not decode IP pool annotation: %v", err), http.StatusInternalServerError)
		return
	}

	// Use the first item from the list if it's not empty
	if len(ipPools) > 0 {
		ipPoolName := ipPools[0]
		a.Logger.Info("Selected IP pool name", zap.String("poolName", ipPoolName))

		// Update the IP pool label to "available"
		if err := a.updateIPPoolLabel(ipPoolName, "available"); err != nil {
			a.Logger.Error("could not update IP pool label", zap.Error(err))
			http.Error(w, fmt.Sprintf("could not update IP pool label: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		a.Logger.Warn("No IP pools found in annotation")
	}

	// // Remove the annotation from the namespace
	// patch := []map[string]interface{}{
	// 	{
	// 		"op":   "remove",
	// 		"path": "/metadata/annotations/cni.projectcalico.org~1ipv4pools", // remove annotation key
	// 	},
	// }

	// if err := a.applyPatch(w, patch); err != nil {
	// 	a.Logger.Error("could not apply patch", zap.Error(err))
	// 	http.Error(w, fmt.Sprintf("could not apply patch: %v", err), http.StatusInternalServerError)
	// }
	// Do not modify the namespace object in the admission response
	admissionResponse.Allowed = true
	a.writeAdmissionResponse(w, admissionResponse)
}

func (a *AdmissionController) applyPatch(w http.ResponseWriter, admissionReviewReq admissionv1.AdmissionReview, patch []map[string]interface{}) error {
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		a.Logger.Error("could not marshal patch", zap.Error(err))
		http.Error(w, fmt.Sprintf("could not marshal patch: %v", err), http.StatusInternalServerError)
		return err
	}

	admissionResponse := &admissionv1.AdmissionResponse{
		UID:       admissionReviewReq.Request.UID,
		Patch:     patchBytes,
		PatchType: func() *admissionv1.PatchType { pt := admissionv1.PatchTypeJSONPatch; return &pt }(),
	}
	a.writeAdmissionResponse(w, admissionResponse)
	return nil
}

func (a *AdmissionController) updateIPPoolLabel(poolName, newStatus string) error {
	ipPool, err := a.Clientset.ProjectcalicoV3().IPPools().Get(context.TODO(), poolName, metav1.GetOptions{})
	if err != nil {
		a.Logger.Error("could not get IP pool", zap.Error(err))
		return fmt.Errorf("could not get IP pool: %v", err)
	}

	labels := normalizeLabels(ipPool.ObjectMeta.Labels)
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["status"] = newStatus
	ipPool.ObjectMeta.Labels = labels

	_, err = a.Clientset.ProjectcalicoV3().IPPools().Update(context.TODO(), ipPool, metav1.UpdateOptions{})
	if err != nil {
		a.Logger.Error("could not update IP pool", zap.Error(err))
		return fmt.Errorf("could not update IP pool: %v", err)
	}
	a.Logger.Info("Successfully updated IP pool label", zap.String("poolName", poolName), zap.String("newStatus", newStatus))
	return nil
}

func (a *AdmissionController) writeAdmissionResponse(w http.ResponseWriter, admissionResponse *admissionv1.AdmissionResponse) {
	a.Logger.Info("Writing admission response")
	admissionReview := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: admissionResponse,
	}

	if err := json.NewEncoder(w).Encode(admissionReview); err != nil {
		a.Logger.Error("could not encode response", zap.Error(err))
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
	a.Logger.Info("Admission review request handled successfully")
}

// Select an available subnet
func (a *AdmissionController) selectAvailableSubnet(subnets []crdv1.IPPool) string {
	for _, subnet := range subnets {
		labels := normalizeLabels(subnet.ObjectMeta.Labels)
		if location, ok := labels["location"]; ok && location == "zone-lhr" {
			if status, ok := labels["status"]; ok && status == "available" {
				a.Logger.Info("Found available subnet", zap.String("subnet", subnet.Name))
				return subnet.Name
			}
		}
	}
	a.Logger.Warn("No available subnet found")
	return ""
}

func normalizeLabels(labels map[string]string) map[string]string {
	normalized := make(map[string]string)
	for key, value := range labels {
		normalized[strings.ToLower(key)] = value
	}
	return normalized
}
