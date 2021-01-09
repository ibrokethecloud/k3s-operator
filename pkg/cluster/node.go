package cluster

import (
	"encoding/base64"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	k3sv1alpha1 "github.com/ibrokethecloud/k3s-operator/pkg/api/v1alpha1"
)

func CheckAndCleanupNode(cluster k3sv1alpha1.Cluster) (err error) {
	kubeConfigByte, err := base64.StdEncoding.DecodeString(cluster.Status.KubeConfig)
	if err != nil {
		return err
	}

	// create a client and get list of nodes //
	config, err := clientcmd.NewClientConfigFromBytes(kubeConfigByte)
	if err != nil {
		return err
	}

	restConfig, err := config.ClientConfig()
	if err != nil {
		return err
	}
	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	// query nodes //
	nodeList, err := clientSet.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	for _, node := range nodeList.Items {
		if _, ok := cluster.Status.NodeStatus[node.Name]; !ok {
			// clean up the node from api server
			err = clientSet.CoreV1().Nodes().Delete(node.Name, &metav1.DeleteOptions{})
			if err != nil {
				return err
			}
		}
	}

	return nil
}
