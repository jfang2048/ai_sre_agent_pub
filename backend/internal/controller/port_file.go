package controller

import (
	"encoding/json"
	"fmt"
	"os"
)

type portFilePayload struct {
	HTTPListen   string `json:"http_listen"`
	HTTPEndpoint string `json:"http_endpoint"`
	GRPCListen   string `json:"grpc_listen"`
}

// WritePortFile writes a small JSON file describing the active listen addresses.
// This is intended for local/dev scripts to pass the actual ports to other processes.
func WritePortFile(path string, httpAddr, grpcAddr string) error {
	payload := portFilePayload{
		HTTPListen:   httpAddr,
		HTTPEndpoint: fmt.Sprintf("http://%s", DialAddr(httpAddr)),
		GRPCListen:   grpcAddr,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
