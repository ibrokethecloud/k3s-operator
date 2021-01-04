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

	"github.com/ibrokethecloud/k3s-operator/pkg/cloudinit"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ec2Instance "github.com/ibrokethecloud/ec2-operator/pkg/api/v1alpha1"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	k3sv1alpha1 "github.com/ibrokethecloud/k3s-operator/pkg/api/v1alpha1"
)

// InstanceTemplateReconciler reconciles a InstanceTemplate object
type InstanceTemplateReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=k3s.io,resources=instancetemplates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k3s.io,resources=instancetemplates/status,verbs=get;update;patch

func (r *InstanceTemplateReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("instancetemplate", req.NamespacedName)

	// Controller reconcile logic
	template := k3sv1alpha1.InstanceTemplate{}

	if err := r.Get(ctx, req.NamespacedName, &template); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "unable to fetch instance template")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if template.ObjectMeta.DeletionTimestamp.IsZero() {
		var err error
		var templateSpecType string
		status := *template.Status.DeepCopy()
		if len(status.InstanceStatus) == 0 {
			status.InstanceStatus = make(map[string]string)
		}
		annotations := make(map[string]string)
		if len(template.GetAnnotations()) != 0 {
			annotations = template.GetAnnotations()
		}

		switch currentStatus := status.Status; currentStatus {
		case "":
			status, templateSpecType, err = r.submitInstances(ctx, template)
			if err != nil {
				log.Error(err, "error during instance creation")
				status.Message = err.Error()
			}
			annotations["instanceType"] = templateSpecType
		case "Submitted":
			// Poll provisioning requests
			status, err = r.fetchInstanceTemplateStatus(ctx, template)
			if err != nil {
				log.Error(err, "error fetching instance status")
				status.Message = err.Error()
			}
		case "Ready":
			// Nodes are provisioned. Mark state of instanceTemplate as Ready //
			log.Info("Instance Template Ready")
			return ctrl.Result{}, nil
		}
		template.Status = status
		template.SetAnnotations(annotations)
		return ctrl.Result{Requeue: true}, r.Update(ctx, &template)
	}
	return ctrl.Result{}, nil
}

func (r *InstanceTemplateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k3sv1alpha1.InstanceTemplate{}).
		Complete(r)
}

func (r *InstanceTemplateReconciler) submitInstances(ctx context.Context,
	template k3sv1alpha1.InstanceTemplate) (status k3sv1alpha1.InstanceTemplateStatus, templateSpecType string, err error) {
	// InstanceTemplate can have only one type of provider //
	status = template.Status
	instanceType := 0
	if template.Spec.InstanceSpec.AWSSpec != nil {
		instanceType = instanceType + 1
	}
	if template.Spec.InstanceSpec.CustomSpec != nil {
		instanceType = instanceType + 1
	}

	if instanceType != 1 {
		return status, "", fmt.Errorf("invalid InstanceSpec, only 1 provider type is needed")
	}

	if template.Spec.InstanceSpec.CustomSpec != nil {
		// CustomSpec so no actual provisioning is needed. Just update status and pass along //
		status.Message = "custom nodes so no provisioning performed. assume nodes are ready"
		status.Status = "Ready"
		status.Provisioned = true
		if len(template.Spec.InstanceSpec.CustomSpec.NodeName) > 0 {
			status.InstanceStatus[template.Spec.InstanceSpec.CustomSpec.NodeName] = template.Spec.InstanceSpec.CustomSpec.Address
		} else {
			status.InstanceStatus[template.Spec.InstanceSpec.CustomSpec.Address] = template.Spec.InstanceSpec.CustomSpec.Address
		}
		templateSpecType = "Custom"
		return status, templateSpecType, nil
	}

	if template.Spec.InstanceSpec.AWSSpec != nil {
		// provision AWS Instances now //
		templateSpecType = "AWS"
		status, err = r.submitAWSInstances(ctx, template)
		if err != nil {
			return status, templateSpecType, err
		}
	}

	return status, templateSpecType, nil
}

func (r *InstanceTemplateReconciler) submitAWSInstances(ctx context.Context,
	template k3sv1alpha1.InstanceTemplate) (status k3sv1alpha1.InstanceTemplateStatus,
	err error) {
	// merge cloudInit to add new ssh keys //
	status = template.Status
	mergedCloudInit, err := cloudinit.AddSSHUserToYaml(template.Spec.InstanceSpec.AWSSpec.UserData,
		template.Spec.User, template.Spec.Group, template.Annotations["pubKey"])
	if err != nil {
		return status, err
	}
	labels := template.GetLabels()
	labels["instanceTemplate"] = template.Name
	for i := 0; i < template.Spec.Count; i++ {
		// submit an instance creation request //
		instance := &ec2Instance.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%d", template.Name, i),
				Namespace: template.Namespace,
				Labels:    labels,
			},
			Spec: *template.Spec.InstanceSpec.AWSSpec,
		}
		instance.Spec.UserData = mergedCloudInit
		// Create ec2 instance along with update ownership reference //
		if _, err = controllerutil.CreateOrUpdate(ctx, r.Client, instance, func() error {
			return controllerutil.SetControllerReference(&template, instance, r.Scheme)
		}); err != nil {
			return status, err
		}

	}

	// instance requests created //
	status.Status = "Submitted"
	return status, nil
}

// poll instances and check if they are ready //
func (r *InstanceTemplateReconciler) fetchInstanceTemplateStatus(ctx context.Context,
	template k3sv1alpha1.InstanceTemplate) (status k3sv1alpha1.InstanceTemplateStatus, err error) {
	// cloud provisioned instances by the individual controller will have Status "provisioned" //
	status = template.Status
	instanceType, ok := template.Annotations["instanceType"]
	if !ok {
		return status, fmt.Errorf("annotation instanceType not set")
	}
	switch instanceType {
	case "AWS":
		status, err = r.fetchAWSInstances(ctx, template)
		// Additional cloud types to be added later //
	}

	return status, err
}

// fetch AWSInstanceInfo //
func (r *InstanceTemplateReconciler) fetchAWSInstances(ctx context.Context,
	template k3sv1alpha1.InstanceTemplate) (status k3sv1alpha1.InstanceTemplateStatus, err error) {
	status = template.Status
	var ec2List ec2Instance.InstanceList
	provisionedCount := 0
	err = r.List(ctx, &ec2List, client.MatchingLabels{"instanceTemplate": template.Name})
	if err != nil {
		return status, err
	}
	// lets query all instances and update the status map
	if len(ec2List.Items) > 0 {
		for _, instance := range ec2List.Items {
			if instance.Status.Status == "provisioned" {
				if instance.Spec.PublicIPAddress {
					status.InstanceStatus[instance.Name] = instance.Status.PublicIP
				} else {
					status.InstanceStatus[instance.Name] = instance.Status.PrivateIP
				}
				provisionedCount++
			}
		}
	} else {
		return status, fmt.Errorf("did not find any ec2 instances")
	}

	if provisionedCount == template.Spec.Count {
		status.Status = "Ready"
		status.Provisioned = true
		status.Message = ""
	}

	return status, nil
}
