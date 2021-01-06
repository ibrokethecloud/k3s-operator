/*
Copyright 2020 The Kubernetes authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"time"

	"gopkg.in/yaml.v2"
	yaml2 "sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k3sv1alpha1 "github.com/ibrokethecloud/k3s-operator/pkg/api/v1alpha1"
	"github.com/ibrokethecloud/k3s-operator/pkg/kubeconfig"
	"github.com/ibrokethecloud/k3s-operator/pkg/ssh"
	"github.com/ibrokethecloud/k3s-operator/pkg/template"
)

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	client.Client
	Log              logr.Logger
	Scheme           *runtime.Scheme
	DefaultConfig    string
	DefaultNamespace string
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

var DefaultConfigFile = "/etc/rancher/k3s"
var DefaultKubeConfig = "/etc/rancher/k3s/k3s.yaml"
var DefaultSSHPort = "22"

// +kubebuilder:rbac:groups=k3s.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k3s.io,resources=clusters/status,verbs=get;update;patch

func (r *ClusterReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("cluster", req.NamespacedName)

	cluster := k3sv1alpha1.Cluster{}

	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		// object doesnt exist so ignored
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch cluster")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// lets process the cluster object now //
	if cluster.ObjectMeta.DeletionTimestamp.IsZero() {
		var err error
		clusterStatus := *cluster.Status.DeepCopy()
		//var clusterStatus k3sv1alpha1.ClusterStatus
		if len(clusterStatus.NodeStatus) == 0 {
			clusterStatus.NodeStatus = make(map[string]string)
		}
		//clusterStatus := *currentStatus
		annotations := make(map[string]string)
		if len(cluster.GetAnnotations()) != 0 {
			annotations = cluster.GetAnnotations()
		}

		switch status := clusterStatus.Status; status {
		case "":
			// first pass so lets gen the ssh key //
			log.Info("Creating ssh key")
			keyPair, err := ssh.NewKeyPair()
			if err != nil {
				log.Error(err, "error generating ssh key.. requeue request")
				return ctrl.Result{Requeue: true}, nil
			}
			token := generateRandomString(16)
			annotations["token"] = token
			annotations["pubKey"] = base64.StdEncoding.EncodeToString(keyPair.PublicKey)
			annotations["privateKey"] = base64.StdEncoding.EncodeToString(keyPair.PrivateKey)
			cluster.SetAnnotations(annotations)
			clusterStatus.Status = "SshKeyGenerated"
		case "SshKeyGenerated":
			log.Info("Creating Instance Pool requests")
			clusterStatus, err = r.createInstancePools(ctx, &cluster)
			if err != nil {
				log.Error(err, "error during instance pool creation")
				clusterStatus.Message = err.Error()
			} else {
				clusterStatus.Message = ""
			}
		case "InstancesPoolsCreated":
			log.Info("Checking Instance Pools status")
			var ready bool
			clusterStatus, err, ready = r.checkInstancePoolsReady(ctx, cluster)
			if err != nil {
				log.Error(err, "error during instance pool check")
				clusterStatus.Message = err.Error()
			}

			if !ready {
				clusterStatus.Message = "instance pools not yet ready.. requeue"
			} else {
				clusterStatus.Message = ""
			}

		case "InstancesPoolsReady":
			log.Info("Instance Pools Ready")
			// identifyLeader method
			var leader, leaderPool string
			clusterStatus, err, leader, leaderPool = r.identifyLeader(ctx, cluster)
			if err != nil {
				log.Error(err, "error during identification of a leader node")
				clusterStatus.Message = err.Error()
			} else {
				annotations["leader"] = leader
				annotations["leaderPool"] = leaderPool
				cluster.SetAnnotations(annotations)
				clusterStatus.Message = ""
			}
		case "ProvisionK3s":
			// provision the cluster
			log.Info("Provisioning Cluster")
			clusterStatus, err = r.manageK3sCluster(ctx, cluster)
			if err != nil {
				log.Error(err, "error during provisioning k3s cluster")
				clusterStatus.Message = err.Error()
			} else {
				clusterStatus.Message = ""
			}
		case "K3sReady":
			// fetch the kubeconfig for the cluster //
			log.Info("K3s ready")
			clusterStatus, err = r.fetchKubeConfig(ctx, cluster)
			if err != nil {
				log.Error(err, "error fetching kubeconfig")
				clusterStatus.Message = err.Error()
			} else {
				clusterStatus.Message = ""
			}
		case "Running":
			log.Info("Cluster running")
			return ctrl.Result{}, nil
		}

		cluster.Status = clusterStatus
		// Need to Requeue since we need to ensure that process is only stopped
		// when cluster is Running
		return ctrl.Result{Requeue: true}, r.Update(ctx, &cluster)
	}
	return ctrl.Result{}, nil
}

func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k3sv1alpha1.Cluster{}).
		Complete(r)
}

// createInstancePools will create InstancePool Objects based on request //
func (r *ClusterReconciler) createInstancePools(ctx context.Context, cluster *k3sv1alpha1.Cluster) (status k3sv1alpha1.ClusterStatus, err error) {
	status = cluster.Status
	labels := cluster.GetLabels()
	if len(labels) == 0 {
		labels = make(map[string]string)
	}
	labels["clusterName"] = cluster.Name
	for _, instancePool := range cluster.Spec.InstancePools {
		ipool := &k3sv1alpha1.InstanceTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", cluster.Name, instancePool.Name),
				Namespace: cluster.Namespace,
				Labels:    labels,
			},
			Spec: instancePool,
		}

		// If no private Key is specified we
		if len(ipool.Spec.SshPrivateKey) == 0 {
			ipool.Spec.SshPrivateKey = cluster.Annotations["privateKey"]
			// also pass pub key to instance template so it can be injected via cloud-init
			if pubKey, ok := cluster.Annotations["pubKey"]; ok {
				annotations := make(map[string]string)
				annotations["pubKey"] = pubKey
				ipool.SetAnnotations(annotations)
			}
		}
		ipool.Status.InstanceStatus = make(map[string]string)

		// Update the owner reference along with creating the instancePool Object
		if _, err = controllerutil.CreateOrUpdate(ctx, r.Client, ipool, func() error {
			return controllerutil.SetControllerReference(cluster, ipool, r.Scheme)
		}); err != nil {
			return status, err
		}
	}

	status.Status = "InstancesPoolsCreated"
	return status, nil
}

// query api server to check if instance pool exists and then subsequent status of the same //
func (r *ClusterReconciler) fetchInstancePools(ctx context.Context,
	ipName string, ipNamespace string) (ip *k3sv1alpha1.InstanceTemplate, err error) {
	ip = &k3sv1alpha1.InstanceTemplate{}
	err = r.Get(ctx, types.NamespacedName{Name: ipName, Namespace: ipNamespace}, ip)
	return ip, err
}

// check if instancePools are ready and keep polling until they are ready //
func (r *ClusterReconciler) checkInstancePoolsReady(ctx context.Context,
	cluster k3sv1alpha1.Cluster) (status k3sv1alpha1.ClusterStatus, err error, ready bool) {
	ready = true
	status = cluster.Status
	for _, instancePool := range cluster.Spec.InstancePools {
		ipoolName := fmt.Sprintf("%s-%s", cluster.Name, instancePool.Name)
		ip, err := r.fetchInstancePools(ctx, ipoolName, cluster.Namespace)
		if err != nil {
			return status, err, ready
		}
		ready = ready && ip.Status.Provisioned
	}

	if !ready {
		return status, nil, ready
	}
	// update status and send reply //
	status.Status = "InstancesPoolsReady"
	return status, nil, ready
}

func (r *ClusterReconciler) identifyLeader(ctx context.Context,
	cluster k3sv1alpha1.Cluster) (status k3sv1alpha1.ClusterStatus, err error, leader string,
	leaderPool string) {
	status = cluster.Status
	for _, instancePool := range cluster.Spec.InstancePools {
		ipoolName := fmt.Sprintf("%s-%s", cluster.Name, instancePool.Name)
		ip, err := r.fetchInstancePools(ctx, ipoolName, cluster.Namespace)
		if err != nil {
			return status, err, leader, leaderPool
		}
		if ip.Spec.Role == "server" {
			// Will Return IP Address of Leader //
			instanceType := ip.Annotations["instanceType"]
			if instanceType == "Custom" {
				for _, leader = range ip.Status.InstanceStatus {

				}
			} else {
				leader = ip.Status.InstanceStatus[ipoolName+"-0"]
			}
			leaderPool = ipoolName
		}
	}
	status.Status = "ProvisionK3s"
	return status, err, leader, leaderPool
}

func (r *ClusterReconciler) manageK3sCluster(ctx context.Context,
	cluster k3sv1alpha1.Cluster) (status k3sv1alpha1.ClusterStatus, err error) {
	status = cluster.Status
	// First fetch default Config //
	config, err := r.getConfig(ctx)

	if err != nil {
		return status, err
	}
	bootStep, ok := config.Data["BootURL"]
	if !ok {
		bootStep = ""
	}

	extraSteps, ok := config.Data["ExtraSteps"]
	if !ok {
		extraSteps = ""
	}

	checkConfigFile, ok := config.Data["DefaultConfigFile"]
	if ok {
		DefaultConfigFile = checkConfigFile
	}

	leader := cluster.Annotations["leader"]
	leaderPool := cluster.Annotations["leaderPool"]
	_, ok = cluster.Status.NodeStatus[leader]
	if !ok {
		// provision leader //
		executeCommand, err := template.GenerateCommand(bootStep, extraSteps, leader, "server")
		if err != nil {
			return status, err
		}

		leaderPoolTemplate, err := r.fetchInstancePools(ctx, leaderPool, cluster.Namespace)
		if err != nil {
			return status, err
		}

		remoteConnection, err := ssh.NewRemoteConnection(leader+":"+DefaultSSHPort, leaderPoolTemplate.Spec.User,
			leaderPoolTemplate.Spec.SshPrivateKey)

		if err != nil {
			return status, err
		}

		//MergeToken into config file/

		// empty server string for the first node //
		mergedConfig, err := generateMergedConfig(cluster.Spec.Config, cluster.Annotations["token"], "",
			leaderPoolTemplate.Spec.Labels, leaderPoolTemplate.Spec.Taints, leader)

		err = remoteConnection.RemoteFile("/tmp/config.yaml", mergedConfig)
		if err != nil {
			return status, err
		}
		remoteCopy := fmt.Sprintf("sudo mkdir -p %s && sudo cp /tmp/config.yaml %s", DefaultConfigFile, DefaultConfigFile)
		_, err = remoteConnection.Remote(remoteCopy)
		if err != nil {
			return status, err
		}
		err = remoteConnection.RemoteFile("/tmp/boot.sh", executeCommand.String())
		if err != nil {
			return status, err
		}

		_, err = remoteConnection.Remote("sudo su -c 'sh /tmp/boot.sh'")
		if err != nil {
			return status, err
		}
	}

	// Update status with Leader Pool //
	status.NodeStatus[leader] = "ready"
	leaderEndpoint := fmt.Sprintf("https://%s:6443", leader)
	for _, clusterPools := range cluster.Spec.InstancePools {
		poolName := fmt.Sprintf("%s-%s", cluster.Name, clusterPools.Name)
		poolTemplate, err := r.fetchInstancePools(ctx, poolName, cluster.Namespace)
		if err != nil {
			return status, err
		}
		for node, address := range poolTemplate.Status.InstanceStatus {
			_, ok := cluster.Status.NodeStatus[node]
			if !ok {
				executeCommand, err := template.GenerateCommand(bootStep, extraSteps, node, poolTemplate.Spec.Role)
				if err != nil {
					return status, err
				}
				// node not present to lets configure it //
				remoteConnection, err := ssh.NewRemoteConnection(address+":"+DefaultSSHPort, poolTemplate.Spec.User,
					poolTemplate.Spec.SshPrivateKey)
				if err != nil {
					return status, err
				}

				mergedConfig, err := generateMergedConfig(cluster.Spec.Config, cluster.Annotations["token"], leaderEndpoint,
					poolTemplate.Spec.Labels, poolTemplate.Spec.Taints, address)
				err = remoteConnection.RemoteFile("/tmp/config.yaml", mergedConfig)
				if err != nil {
					return status, err
				}
				remoteCopy := fmt.Sprintf("sudo mkdir -p %s && sudo cp /tmp/config.yaml %s", DefaultConfigFile, DefaultConfigFile)
				_, err = remoteConnection.Remote(remoteCopy)
				if err != nil {
					return status, err
				}

				err = remoteConnection.RemoteFile("/tmp/boot.sh", executeCommand.String())
				if err != nil {
					return status, err
				}

				_, err = remoteConnection.Remote("sudo su -c 'sh /tmp/boot.sh'")
				if err != nil {
					return status, err
				}
			}
			status.NodeStatus[node] = "ready"
		}
	}
	// All nodes are now ready //
	status.Status = "K3sReady"
	return status, err
}

func (r *ClusterReconciler) getConfig(ctx context.Context) (config *corev1.ConfigMap, err error) {
	config = &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: r.DefaultConfig, Namespace: r.DefaultNamespace}, config)
	return config, client.IgnoreNotFound(err)
}

func generateRandomString(size int) (random string) {
	var letters = []rune("abcdefghijklmnopqrstuvwxyz")
	b := make([]rune, size)
	for i := range b {
		b[i] = letters[rand.Intn(len(letters))]
	}
	random = string(b)
	return random
}

func generateMergedConfig(config string, token string, server string,
	labels []string, taints []string, nodeName string) (mergedConfig string, err error) {
	configStruct := make(map[string]interface{})
	err = yaml2.Unmarshal([]byte(config), configStruct)
	if err != nil {
		return mergedConfig, err
	}
	if len(server) != 0 {
		configStruct["server"] = server
	} else {
		configStruct["cluster-init"] = "true"
	}

	configStruct["token"] = token
	configStruct["node-name"] = nodeName
	if len(labels) != 0 {
		orgLabels := configStruct["node-label"].([]string)
		orgLabels = append(orgLabels, labels...)
		configStruct["node-label"] = orgLabels
	}
	if len(taints) != 0 {
		orgTaints := configStruct["node-taint"].([]string)
		orgTaints = append(orgTaints, taints...)
		configStruct["node-taint"] = orgTaints
	}
	mergedByte, err := yaml.Marshal(configStruct)
	if err == nil {
		mergedConfig = string(mergedByte)
	}

	return mergedConfig, err
}

func (r *ClusterReconciler) fetchKubeConfig(ctx context.Context, cluster k3sv1alpha1.Cluster) (status k3sv1alpha1.ClusterStatus,
	err error) {
	status = cluster.Status
	leader := cluster.Annotations["leader"]
	leaderPool := cluster.Annotations["leaderPool"]
	poolTemplate, err := r.fetchInstancePools(ctx, leaderPool, cluster.Namespace)
	if err != nil {
		return status, err
	}
	remoteConnection, err := ssh.NewRemoteConnection(leader+":"+DefaultSSHPort, poolTemplate.Spec.User, poolTemplate.Spec.SshPrivateKey)
	if err != nil {
		return status, err
	}
	output, err := remoteConnection.Remote("sudo cat " + DefaultKubeConfig + " | base64 -w 0")
	if err != nil {
		return status, err
	}

	patchedKubeCfg, err := patchKubeConfig(output, leader)
	if err != nil {
		return status, err
	}
	status.Status = "Running"
	status.KubeConfig = patchedKubeCfg
	return status, nil
}

func patchKubeConfig(encodedKubeConfig []byte, leader string) (patchedCfg string, err error) {
	kubeCfg := kubeconfig.KubectlConfig{}
	kubeConfig, err := base64.StdEncoding.DecodeString(string(encodedKubeConfig))
	leaderAddr := fmt.Sprintf("https://%s:6443", leader)
	err = yaml.Unmarshal(kubeConfig, &kubeCfg)
	if err != nil {
		return patchedCfg, err
	}

	for _, v := range kubeCfg.Clusters {
		if v.Name == "default" {
			v.Cluster.Server = leaderAddr
		}
	}

	newKubeCfg, err := yaml.Marshal(kubeCfg)
	if err == nil {
		patchedCfg = base64.StdEncoding.EncodeToString(newKubeCfg)
	}

	return patchedCfg, err
}
