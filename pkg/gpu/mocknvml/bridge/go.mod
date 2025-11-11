module github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/bridge

go 1.22

require (
	github.com/NVIDIA/go-nvml v0.13.0-1
	github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine v0.0.0
)

require github.com/google/uuid v1.6.0 // indirect

replace github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine => ../engine
