package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	crdv1 "github.com/projectcalico/api/pkg/apis/projectcalico/v3"
	crdclient "github.com/projectcalico/api/pkg/client/clientset_generated/clientset"
)

type IPPool struct {
	Metadata struct {
		Name   string
		Labels map[string]string
	} `json:"metadata"`
}

type AdmissionController struct {
	clientset *kubernetes.Clientset
	crdClient *crdclient.Clientset
}

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

func (a *AdmissionController) getIPPoolsFromCluster() ([]crdv1.IPPool, error) {
	// Fetch IPPools from the Kubernetes cluster using the CRD client
	ippoolList, err := a.crdClient.ProjectcalicoV3().IPPools().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	return ippoolList.Items, nil
}

func main() {
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// Build the Kubernetes client configuration
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		fmt.Println("Error building kubeconfig:", err)
		os.Exit(1)
	}

	// Create the Kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Println("Error creating Kubernetes client:", err)
		os.Exit(1)
	}

	// Create the CRD client to interact with custom resources like Calico's IPPool
	crdClient, err := crdclient.NewForConfig(config)
	if err != nil {
		fmt.Println("Error creating CRD client:", err)
		os.Exit(1)
	}

	// Initialize the AdmissionController with the clientset and CRD client
	controller := &AdmissionController{
		clientset: clientset,
		crdClient: crdClient,
	}

	// Fetch IP pools from the Kubernetes cluster using the CRD API
	ipPools, err := controller.getIPPoolsFromCluster()
	if err != nil {
		fmt.Println("Error getting IP pools:", err)
		os.Exit(1)
	}

	// Test the logic with real cluster data
	availablePool := controller.selectAvailableSubnet(ipPools)
	if availablePool == "" {
		fmt.Println("No available subnets found.")
	} else {
		fmt.Printf("Selected IP Pool: %s\n", availablePool)
	}
}
