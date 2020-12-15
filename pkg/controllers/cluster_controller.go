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
	"fmt"

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
)

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=k3s.my.domain,resources=clusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k3s.my.domain,resources=clusters/status,verbs=get;update;patch

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
		clusterStatus := *currentStatus
		switch status := currentStatus.Status; status {
		case "":
			// first pass so lets gen the ssh key //
			log.Info("Creating ssh key")
			keyPair, err := ssh.NewKeyPair()
			if err != nil {
				log.Info("Error generating ssh key.. requeue request")
				return ctrl.Result{Requeue: true}, nil
			}
			annotations := make(map[string]string)
			annotations = cluster.Annotations
			annotations["pubKey"] = string(keyPair.PublicKey)
			annotations["privateKey"] = string(keyPair.PrivateKey)
			cluster.Annotations = annotations
			clusterStatus.Status = "SshKeyGenerated"
		case "SshKeyGenerated":
			log.Info("Creating Instance Pool requests")
			clusterStatus, err = r.createInstancePools(ctx, &cluster)
			if err != nil {
				return ctrl.Result{}, err
			}
		case "InstancesPoolsCreated":
			log.Info("Instance Pools Created")
		case "InstancesPoolsReady":
			log.Info("Instance Pools Ready")
		case "K3sReady":
			log.Info("K3s ready")
		case "Running":
			log.Info("Cluster running")
			return ctrl.Result{}, nil
		}

		cluster.Status = clusterStatus
		return ctrl.Result{}, r.Update(ctx, &cluster)
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

		// Update public key for this pool //
		ipool.Spec.SshKey = append(ipool.Spec.SshKey, cluster.Annotations["pubKey"])

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
