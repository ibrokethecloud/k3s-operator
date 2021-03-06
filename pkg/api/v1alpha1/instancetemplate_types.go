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

package v1alpha1

import (
	ec2Instance "github.com/ibrokethecloud/ec2-operator/pkg/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// InstanceTemplateSpec defines the desired state of InstanceTemplate
type InstanceTemplateSpec struct {
	Role          string       `json:"role"`
	Count         int          `json:"count"`
	InstanceSpec  InstanceSpec `json:"instanceSpec"`
	User          string       `json:"user"`
	Group         string       `json:"group"`
	SshPrivateKey string       `json:"sshPrivateKey,omitempty"`
	Name          string       `json:"name"`
	Taints        []string     `json:"taints,omitempty"`
	Labels        []string     `json:"labels,omitempty"`
}

// InstanceSpec is the place holder for the various cloud specific Instance Specs
type InstanceSpec struct {
	AWSSpec    *ec2Instance.InstanceSpec `json:"aws,omitempty"`
	CustomSpec *CustomInstance           `json:"custom,omitempty"`
}

type CustomInstance struct {
	Address  string `json:"address"`
	NodeName string `json:"nodeName,omitempty"`
}

// InstanceTemplateStatus defines the observed state of InstanceTemplate
type InstanceTemplateStatus struct {
	Provisioned    bool              `json:"provisioned"`
	InstanceStatus map[string]string `json:"instanceStatus"` // contains name and address of instance
	Status         string            `json:"status"`
	Message        string            `json:"message"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.status",description="provisioning status of template"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.message",description="Error Message"
// InstanceTemplate is the Schema for the instancetemplates API
type InstanceTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InstanceTemplateSpec   `json:"spec,omitempty"`
	Status InstanceTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InstanceTemplateList contains a list of InstanceTemplate
type InstanceTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InstanceTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InstanceTemplate{}, &InstanceTemplateList{})
}
