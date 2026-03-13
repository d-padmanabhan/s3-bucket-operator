/*
Copyright 2026.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// S3BucketSpec defines the desired state of S3Bucket
type S3BucketSpec struct {
	// bucketName is the S3 bucket name (globally unique).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`
	BucketName string `json:"bucketName"`

	// region is the AWS region where the bucket should live (e.g. us-east-1).
	// +kubebuilder:validation:Required
	Region string `json:"region"`

	// versioning enables S3 versioning when true.
	// +kubebuilder:default=false
	// +optional
	Versioning bool `json:"versioning,omitempty"`

	// tags are applied to the bucket.
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// forceDelete, when true, attempts to delete all objects/versions before deleting the bucket.
	// +kubebuilder:default=false
	// +optional
	ForceDelete bool `json:"forceDelete,omitempty"`
}

// S3BucketStatus defines the observed state of S3Bucket.
type S3BucketStatus struct {
	// phase is a simple human-readable summary.
	// +kubebuilder:validation:Enum=Creating;Ready;Deleting;Failed
	// +optional
	Phase string `json:"phase,omitempty"`

	// bucketARN is the AWS ARN of the bucket, when known.
	// +optional
	BucketARN string `json:"bucketARN,omitempty"`

	// observedGeneration is the .metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the S3Bucket resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="BucketName",type="string",JSONPath=".spec.bucketName"
// +kubebuilder:printcolumn:name="Region",type="string",JSONPath=".spec.region"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// S3Bucket is the Schema for the s3buckets API
type S3Bucket struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of S3Bucket
	// +required
	Spec S3BucketSpec `json:"spec"`

	// status defines the observed state of S3Bucket
	// +optional
	Status S3BucketStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// S3BucketList contains a list of S3Bucket
type S3BucketList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []S3Bucket `json:"items"`
}

func init() {
	SchemeBuilder.Register(&S3Bucket{}, &S3BucketList{})
}
