package main

import (
	"log"
	"net/http"

	"admission-controller-02/pkg/admission"
)

func main() {
	http.HandleFunc("/mutate", admission.HandleAdmissionReview)
	server := &http.Server{
		Addr: ":8443",
	}
	log.Println("Starting webhook server on port 8443...")
	if err := server.ListenAndServeTLS("/tls/tls.crt", "/tls/tls.key"); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
