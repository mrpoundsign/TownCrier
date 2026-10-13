package main

import (
	"fmt"
	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

func main() {
	fmt.Println("Sherpa ONNX minimal test...")
	cfg := &sherpa.OfflineTtsConfig{}
	fmt.Printf("Config created: %+v\n", cfg)
}
