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
	b64 "encoding/base64"
	"encoding/json"
	"fmt"

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
		templateStatus := *template.Status.DeepCopy()
		status := k3sv1alpha1.InstanceTemplateStatus{}
		annotations := make(map[string]string)
		if len(template.GetAnnotations()) != 0 {
			annotations = template.GetAnnotations()
		}

		switch currentStatus := templateStatus.Status; currentStatus {
		case "":
			status, templateSpecType, err = r.submitInstances(ctx, template)
			if err != nil {
				status.Message = err.Error()
			}
			annotations["instanceType"] = templateSpecType
		case "Submitted":
			// Poll provisioning requests
		case "Ready":
			// Nodes are provisioned. Mark state of instanceTemplate as Ready //
			log.Info("Instance Template Ready")
			return ctrl.Result{}, nil
		}
		template.Status = status
		template.SetAnnotations(annotations)
		return ctrl.Result{Requeue: true}, r.Update(ctx, &instance)
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
	mergedCloudInit, err := mergeCloudInit(template.Spec.InstanceSpec.AWSSpec.UserData,
		template.Annotations["pubKey"])
	if err != nil {
		return status, err
	}

	for i := 0; i < template.Spec.Count; i++ {
		// submit an instance creation request //
		instance := &ec2Instance.Instance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%d", template.Name, i),
				Namespace: template.Namespace,
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

func mergeCloudInit(inputCloudInit string, pubKey string) (outputCloudInit string, err error) {
	cloudInitMap := make(map[string]interface{})
	decodedCloudInitByte, err := b64.StdEncoding.DecodeString(inputCloudInit)
	if err != nil {
		return outputCloudInit, err
	}
	err = json.Unmarshal(decodedCloudInitByte, &cloudInitMap)
	if err != nil {
		return outputCloudInit, err
	}
	var sshKeys []string
	keys, ok := cloudInitMap["ssh_authorized_keys"]
	if ok {
		sshKeys = keys.([]string)
	}
	sshKeys = append(sshKeys, pubKey)
	cloudInitMap["ssh_authorized_keys"] = sshKeys
	outputCloudInitByte, err := json.Marshal(cloudInitMap)
	if err != nil {
		return outputCloudInit, err
	}

	outputCloudInit = b64.StdEncoding.EncodeToString(outputCloudInitByte)
	return outputCloudInit, err
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

}
