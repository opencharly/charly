package gpu

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

// data.go — plugin-gpu's OWN detection data tables (K5 seam-death: device_patterns /
// gpu_vendors / pci_class_labels moved OUT of charly-core's embedded charly.yml). Core's
// "must-stay, `charly doctor` reads device_patterns" rationale (the former devices.go/gpu.go
// header claims) does not survive: plugin-gpu is the ONLY thing that actually consumes these
// tables for detection, and candy/plugin-doctor now reaches verb:gpu peer-to-peer instead of
// threading the tables through core — so this plugin is the ONE data source (R3), not core.

//go:embed data.yml
var embeddedData []byte

type gpuDataDoc struct {
	DevicePatterns []string          `yaml:"device_patterns"`
	GpuVendors     map[string]string `yaml:"gpu_vendors"`
	PciClassLabels map[string]string `yaml:"pci_class_labels"`
}

var gpuData = parseEmbeddedData()

func parseEmbeddedData() gpuDataDoc {
	var doc gpuDataDoc
	if err := yaml.Unmarshal(embeddedData, &doc); err != nil {
		panic("plugin-gpu: embedded data.yml unparseable: " + err.Error())
	}
	if len(doc.DevicePatterns) == 0 || len(doc.GpuVendors) == 0 || len(doc.PciClassLabels) == 0 {
		panic("plugin-gpu: embedded data.yml missing a directive")
	}
	return doc
}
