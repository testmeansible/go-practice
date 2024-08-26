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

func NewAdmissionController() (*AdmissionController, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("could not get in-cluster config: %v", err)
	}

	// config, err := clientcmd.BuildConfigFromFlags("", "C:\\Users\\aaaaaa\\Desktop\\kube\\config")
	// if err != nil {
	// 	panic(err.Error())
	// }

	clientset, err := clientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("could not create Calico clientset: %v", err)
	}

	k8sClientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("could not create Kubernetes clientset: %v", err)
	}

	logger, _ := zap.NewProduction() // Create a logger
	defer logger.Sync()              // Flushes buffer, if any

	return &AdmissionController{
		Clientset:    clientset,
		K8sClientset: k8sClientset,
		Logger:       logger,
	}, nil
}

func normalizeLabels(labels map[string]string) map[string]string {
	normalized := make(map[string]string)
	for key, value := range labels {
		normalized[strings.ToLower(key)] = value
	}
	return normalized
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
		if admissionReviewReq.Request.Operation == admissionv1.Create {
			// Handle namespace creation logic
			a.Logger.Info("Handling namespace creation")
			// Fetch the available IP pools
			ipPools, err := a.Clientset.ProjectcalicoV3().IPPools().List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				a.Logger.Error("could not list IP pools", zap.Error(err))
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
				a.Logger.Warn("No available subnets found")
				admissionResponse.Allowed = false
				admissionResponse.Result = &metav1.Status{
					Message: "No available subnets found.",
				}
				writeAdmissionResponse(w, admissionResponse)
				return
			}
			a.Logger.Info("Selected subnet for namespace", zap.String("subnet", availableSubnet))
			// Step 4: Patch the namespace with the selected IP pool
			annotationValue := fmt.Sprintf(`["%s"]`, availableSubnet)

			patch := []map[string]interface{}{
				{
					"op":    "add",
					"path":  "/metadata/annotations",
					"value": map[string]string{}, // This will create an empty annotations map if it doesn't exist
				},
				{
					"op":    "add",
					"path":  "/metadata/annotations/cni.projectcalico.org~1ipv4pools", // Escaping the "/" character
					"value": annotationValue,
				},
			}

			patchBytes, err := json.Marshal(patch)
			if err != nil {
				a.Logger.Error("could not marshal patch", zap.Error(err))
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
				a.Logger.Error("could not update IP pool label", zap.Error(err))
				admissionResponse.Allowed = false
				admissionResponse.Result = &metav1.Status{
					Message: fmt.Sprintf("could not update IP pool label: %v", err),
				}
				writeAdmissionResponse(w, admissionResponse)
				return
			}

		} else if admissionReviewReq.Request.Operation == admissionv1.Delete {
			// Handle namespace deletion logic
			a.Logger.Info("Handling namespace deletion")
			// Fetch the namespace to get the IP pool annotation
			namespace := admissionReviewReq.Request.Name
			ns, err := a.K8sClientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metav1.GetOptions{})
			if err != nil {
				a.Logger.Error("could not fetch namespace", zap.Error(err))
				http.Error(w, fmt.Sprintf("could not fetch namespace: %v", err), http.StatusInternalServerError)
				return
			}

			ipPoolAnnotation := ns.Annotations["cni.projectcalico.org/ipv4pools"]
			if ipPoolAnnotation == "" {
				a.Logger.Warn("No IP pool annotation found")
				writeAdmissionResponse(w, admissionResponse)
				return
			}

			// Parse the IP pool name from the annotation
			var ipPools []string
			if err := json.Unmarshal([]byte(ipPoolAnnotation), &ipPools); err != nil {
				a.Logger.Error("could not parse IP pool annotation", zap.Error(err))
				http.Error(w, fmt.Sprintf("could not parse IP pool annotation: %v", err), http.StatusInternalServerError)
				return
			}
			ipPool := ipPools[0] // Assuming single IP pool per namespace

			// Mark the IP pool as available by updating the label
			if err := a.updateIPPoolLabel(ipPool, "available"); err != nil {
				a.Logger.Error("could not update IP pool label", zap.Error(err))
				http.Error(w, fmt.Sprintf("could not update IP pool label: %v", err), http.StatusInternalServerError)
				return
			}

			// Remove the annotation from the namespace
			patch := []map[string]interface{}{
				{
					"op":   "remove",
					"path": "/metadata/annotations/cni.projectcalico.org~1ipv4pools", // remove annotation key
				},
			}

			patchBytes, err := json.Marshal(patch)
			if err != nil {
				a.Logger.Error("could not marshal patch", zap.Error(err))
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
		labels := normalizeLabels(subnet.ObjectMeta.Labels)
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

	labels := normalizeLabels(ipPool.ObjectMeta.Labels)

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
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: admissionResponse,
	}

	if err := json.NewEncoder(w).Encode(admissionReview); err != nil {
		http.Error(w, fmt.Sprintf("could not encode response: %v", err), http.StatusInternalServerError)
	}
}
