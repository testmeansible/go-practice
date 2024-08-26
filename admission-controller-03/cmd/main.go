package main

import (
	"fmt"
	"log"
	"net/http"

	"go.uber.org/zap"

	"admission-controller-03/pkg/admission"
)

func main() {
	// Create a logger
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Can't initialize zap logger: %v", err)
	}
	defer logger.Sync() // flushes buffer, if any
	controller, err := admission.NewAdmissionController(logger)
	if err != nil {
		logger.Error("could not create admission controller", zap.Error(err))
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
