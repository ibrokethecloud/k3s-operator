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

	corev1 "github.com/rancher/rancher/pkg/generated/norman/core/v1"

	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ibrokethecloud/k3s-operator/pkg/api/v1alpha1"
	k3sv1alpha1 "github.com/ibrokethecloud/k3s-operator/pkg/api/v1alpha1"
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
		currentStatus := cluster.Status.DeepCopy()
		//var clusterStatus k3sv1alpha1.ClusterStatus
		//clusterStatus.NodeStatus = make(map[string]string)
		clusterStatus := *currentStatus
		annotations := make(map[string]string)
		if len(cluster.GetAnnotations()) != 0 {
			annotations = cluster.GetAnnotations()
		}

		switch status := currentStatus.Status; status {
		case "":
			// first pass so lets gen the ssh key //
			log.Info("Creating ssh key")
			keyPair, err := ssh.NewKeyPair()
			if err != nil {
				log.Info("Error generating ssh key.. requeue request")
				return ctrl.Result{Requeue: true}, nil
			}
			annotations["pubKey"] = base64.StdEncoding.EncodeToString(keyPair.PublicKey)
			annotations["privateKey"] = base64.StdEncoding.EncodeToString(keyPair.PrivateKey)
			cluster.SetAnnotations(annotations)
			clusterStatus.Status = "SshKeyGenerated"
		case "SshKeyGenerated":
			log.Info("Creating Instance Pool requests")
			clusterStatus, err = r.createInstancePools(ctx, &cluster)
			if err != nil {
				return ctrl.Result{}, err
			}
			// break here to test
			return ctrl.Result{}, nil
		case "InstancesPoolsCreated":
			log.Info("Instance Pools Created")
			// checkInstancePoolsReady method
		case "InstancesPoolsReady":
			log.Info("Instance Pools Ready")
			// identifyLeader method
			var leader, leaderPool string
			clusterStatus, err, leader, leaderPool = r.identifyLeader(ctx, cluster)
			if err != nil {
				return ctrl.Result{}, err
			}
			annotations["leader"] = leader
			annotations["leaderPool"] = leaderPool
			cluster.SetAnnotations(annotations)
		case "ProvisionK3s":
			log.Info("Identify Seed Node")
			// Identify Leader and provision //
		case "K3sReady":
			log.Info("K3s ready")
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
func (r *ClusterReconciler) createInstancePools(ctx context.Context, cluster *k3sv1alpha1.Cluster) (status v1alpha1.ClusterStatus, err error) {
	status = *cluster.Status.DeepCopy()
	for _, instancePool := range cluster.Spec.InstancePools {
		ipool := &k3sv1alpha1.InstanceTemplate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", cluster.Name, instancePool.Name),
				Namespace: cluster.Namespace,
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
	err = r.Get(ctx, types.NamespacedName{Name: ipName, Namespace: ipNamespace}, ip)
	return ip, err
}

// check if instancePools are ready and keep polling until they are ready //
func (r *ClusterReconciler) checkInstancePoolsReady(ctx context.Context,
	cluster *k3sv1alpha1.Cluster) (status v1alpha1.ClusterStatus, err error, ready bool) {
	ready = true
	for _, instancePool := range cluster.Spec.InstancePools {
		ipoolName := fmt.Sprintf("%s-%s", cluster.Name, instancePool.Name)
		ip, err := r.fetchInstancePools(ctx, ipoolName, cluster.Namespace)
		if err != nil {
			return status, err, ready
		}
		ready = ready && ip.Status.Provisioned
	}

	// update status and send reply //
	status.Status = "InstancesPoolsReady"
	return status, err, ready
}

func (r *ClusterReconciler) identifyLeader(ctx context.Context,
	cluster k3sv1alpha1.Cluster) (status v1alpha1.ClusterStatus, err error, leader string,
	leaderPool string) {
	for _, instancePool := range cluster.Spec.InstancePools {
		ipoolName := fmt.Sprintf("%s-%s", cluster.Name, instancePool.Name)
		ip, err := r.fetchInstancePools(ctx, ipoolName, cluster.Namespace)
		if err != nil {
			return status, err, leader
		}
		if ip.Spec.Role == "server" {
			// Will Return IP Address of Leader //
			leader = ip.Status.InstanceStatus[ipoolName+"-0"]
			leaderPool = ipoolName
		}
	}
	status.Status = "ProvisionK3s"
	return status, err, leader
}

func (r *ClusterReconciler) manageK3sCluster(ctx context.Context,
	cluster k3sv1alpha1.Cluster) (status v1alpha1.ClusterStatus, err error, ready bool) {
	ready = true
	// First fetch default Config //
	config, err := r.getConfig(ctx)
	if err != nil {
		status.Status = "FetchConfigError"
		return status, err, ready
	}
	bootStep, ok := config.Data["BootStep"]
	if !ok {
		bootStep = ""
	}

	extraSteps, ok := config.Data["ExtraSteps"]
	if !ok {
		extraSteps = ""
	}

	leader := cluster.Annotations["leader"]
	leaderPool := cluster.Annotations["leaderPool"]
	// provision leader //
	executeCommand, err := template.GenerateCommand(bootStep, extraSteps, leader)
	if err != nil {
		status.Status = "LeaderCommandGenerateError"
		return status, err, ready
	}

	leaderPoolTemplate, err := r.fetchInstancePools(ctx, leaderPoolName, cluster.Namespace)
	if err != nil {
		status.Status = "LeaderPoolFetchError"
		return status, err, ready
	}

	remoteConnection, err := ssh.NewRemoteConnection(leader, leaderPoolTemplate.Spec.User,
		leaderPoolTemplate.Spec.SshPrivateKey)

	if err != nil {
		status.Status = "LeaderRemoteConnError"
		return status, err, ready
	}

	return
}

func (r *ClusterReconciler) getConfig(ctx context.Context) (config *corev1.ConfigMap, err error) {
	config = &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: r.DefaultConfig, Namespace: r.DefaultNamespace}, config)
	return config, err
}
