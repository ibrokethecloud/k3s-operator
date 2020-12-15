module github.com/ibrokethecloud/k3s-operator

go 1.13

require (
	github.com/go-logr/logr v0.1.0
	github.com/ibrokethecloud/ec2-operator v0.0.0-20200909043908-30b62dc8600c
	github.com/onsi/ginkgo v1.11.0
	github.com/onsi/gomega v1.8.1
	golang.org/x/crypto v0.0.0-20190820162420-60c769a6c586
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v0.17.2
	sigs.k8s.io/controller-runtime v0.5.0
)
