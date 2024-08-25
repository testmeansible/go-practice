package main

import (
	"fmt"
	"net/http"

	"admission-controller-03/pkg/admission"
)

func main() {
	controller, err := admission.NewAdmissionController()
	if err != nil {
		panic(fmt.Sprintf("Failed to create admission controller: %v", err))
	}

	http.HandleFunc("/mutate", controller.HandleAdmissionReview)
	server := &http.Server{
		Addr: ":8443",
	}
	fmt.Println("Starting webhook server on port 8443...")
	// Update the path to the absolute path on your Windows system
	// certPath := "C:\\Users\\Mansoor\\Desktop\\kube\\admission_controller.crt"
	// keyPath := "C:\\Users\\Mansoor\\Desktop\\kube\\admission_controller.key"

	// Use the default file paths where the secrets are mounted in Kubernetes
	certPath := "/etc/webhook/certs/tls.crt"
	keyPath := "/etc/webhook/certs/tls.key"

	if err := server.ListenAndServeTLS(certPath, keyPath); err != nil {
		panic(fmt.Sprintf("Failed to start server: %v", err))
	}
}
