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

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	awsv1alpha1 "github.com/d-padmanabhan/s3-bucket-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// S3BucketReconciler reconciles a S3Bucket object
type S3BucketReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// newS3Client can be overridden in tests. If nil, a default implementation is used.
	newS3Client func(ctx context.Context, region string) (*s3.Client, error)
}

// +kubebuilder:rbac:groups=aws.techfueled.dev,resources=s3buckets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aws.techfueled.dev,resources=s3buckets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aws.techfueled.dev,resources=s3buckets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the S3Bucket object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *S3BucketReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	bucket := &awsv1alpha1.S3Bucket{}
	if err := r.Get(ctx, req.NamespacedName, bucket); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	const finalizer = "aws.techfueled.dev/s3bucket-finalizer"

	if bucket.DeletionTimestamp != nil {
		if bucket.Status.Phase != "Deleting" {
			bucket.Status.Phase = "Deleting"
			bucket.Status.ObservedGeneration = bucket.Generation
			setCondition(&bucket.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "Deleting",
				Message:            "Deletion in progress",
				ObservedGeneration: bucket.Generation,
			})
			_ = r.Status().Update(ctx, bucket)
		}

		if !controllerutil.ContainsFinalizer(bucket, finalizer) {
			return ctrl.Result{}, nil
		}

		s3c, err := r.getS3Client(ctx, bucket.Spec.Region)
		if err != nil {
			log.Error(err, "failed to build AWS S3 client", "region", bucket.Spec.Region)
			setCondition(&bucket.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "AWSClientInitFailed",
				Message:            err.Error(),
				ObservedGeneration: bucket.Generation,
			})
			_ = r.Status().Update(ctx, bucket)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}

		if err := ensureBucketDeleted(ctx, s3c, bucket.Spec.BucketName, bucket.Spec.ForceDelete); err != nil {
			log.Error(err, "failed to delete bucket", "bucketName", bucket.Spec.BucketName)
			setCondition(&bucket.Status.Conditions, metav1.Condition{
				Type:               "Ready",
				Status:             metav1.ConditionFalse,
				Reason:             "DeleteFailed",
				Message:            err.Error(),
				ObservedGeneration: bucket.Generation,
			})
			_ = r.Status().Update(ctx, bucket)
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}

		controllerutil.RemoveFinalizer(bucket, finalizer)
		if err := r.Update(ctx, bucket); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(bucket, finalizer) {
		controllerutil.AddFinalizer(bucket, finalizer)
		if err := r.Update(ctx, bucket); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	s3c, err := r.getS3Client(ctx, bucket.Spec.Region)
	if err != nil {
		log.Error(err, "failed to build AWS S3 client", "region", bucket.Spec.Region)
		bucket.Status.Phase = "Failed"
		bucket.Status.ObservedGeneration = bucket.Generation
		setCondition(&bucket.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "AWSClientInitFailed",
			Message:            err.Error(),
			ObservedGeneration: bucket.Generation,
		})
		_ = r.Status().Update(ctx, bucket)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	created, err := ensureBucketExists(ctx, s3c, bucket.Spec.BucketName, bucket.Spec.Region)
	if err != nil {
		log.Error(err, "failed to ensure bucket exists", "bucketName", bucket.Spec.BucketName)
		bucket.Status.Phase = "Failed"
		bucket.Status.ObservedGeneration = bucket.Generation
		setCondition(&bucket.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "CreateOrCheckFailed",
			Message:            err.Error(),
			ObservedGeneration: bucket.Generation,
		})
		_ = r.Status().Update(ctx, bucket)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if created {
		bucket.Status.Phase = "Creating"
		bucket.Status.ObservedGeneration = bucket.Generation
		setCondition(&bucket.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "Creating",
			Message:            "Bucket creation requested",
			ObservedGeneration: bucket.Generation,
		})
		bucket.Status.BucketARN = fmt.Sprintf("arn:aws:s3:::%s", bucket.Spec.BucketName)
		_ = r.Status().Update(ctx, bucket)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if err := ensureVersioning(ctx, s3c, bucket.Spec.BucketName, bucket.Spec.Versioning); err != nil {
		log.Error(err, "failed to reconcile bucket versioning", "bucketName", bucket.Spec.BucketName)
		bucket.Status.Phase = "Failed"
		bucket.Status.ObservedGeneration = bucket.Generation
		setCondition(&bucket.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "VersioningSyncFailed",
			Message:            err.Error(),
			ObservedGeneration: bucket.Generation,
		})
		_ = r.Status().Update(ctx, bucket)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if err := ensureTags(ctx, s3c, bucket.Spec.BucketName, bucket.Spec.Tags); err != nil {
		log.Error(err, "failed to reconcile bucket tags", "bucketName", bucket.Spec.BucketName)
		bucket.Status.Phase = "Failed"
		bucket.Status.ObservedGeneration = bucket.Generation
		setCondition(&bucket.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "TagsSyncFailed",
			Message:            err.Error(),
			ObservedGeneration: bucket.Generation,
		})
		_ = r.Status().Update(ctx, bucket)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	bucket.Status.Phase = "Ready"
	bucket.Status.BucketARN = fmt.Sprintf("arn:aws:s3:::%s", bucket.Spec.BucketName)
	bucket.Status.ObservedGeneration = bucket.Generation
	setCondition(&bucket.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Reconciled",
		Message:            "Bucket is reconciled",
		ObservedGeneration: bucket.Generation,
	})

	if err := r.Status().Update(ctx, bucket); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *S3BucketReconciler) getS3Client(ctx context.Context, region string) (*s3.Client, error) {
	if r.newS3Client != nil {
		return r.newS3Client(ctx, region)
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg), nil
}

func setCondition(conditions *[]metav1.Condition, c metav1.Condition) {
	c.LastTransitionTime = metav1.Now()
	meta.SetStatusCondition(conditions, c)
}

func ensureBucketExists(ctx context.Context, s3c *s3.Client, bucketName string, region string) (bool, error) {
	_, err := s3c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucketName)})
	if err == nil {
		return false, nil
	}
	if !isS3NotFound(err) {
		return false, err
	}

	in := &s3.CreateBucketInput{Bucket: aws.String(bucketName)}
	// For us-east-1, LocationConstraint must be omitted.
	if region != "us-east-1" {
		in.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}

	_, err = s3c.CreateBucket(ctx, in)
	if err == nil {
		return true, nil
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "BucketAlreadyOwnedByYou" {
		return false, nil
	}

	return false, err
}

func ensureVersioning(ctx context.Context, s3c *s3.Client, bucketName string, enabled bool) error {
	out, err := s3c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucketName)})
	if err != nil {
		return err
	}

	want := s3types.BucketVersioningStatusSuspended
	if enabled {
		want = s3types.BucketVersioningStatusEnabled
	}

	if out.Status == want {
		return nil
	}

	_, err = s3c.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucketName),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: want,
		},
	})
	return err
}

func ensureTags(ctx context.Context, s3c *s3.Client, bucketName string, tags map[string]string) error {
	if len(tags) == 0 {
		// Best-effort delete (no-op if none set).
		_, err := s3c.DeleteBucketTagging(ctx, &s3.DeleteBucketTaggingInput{Bucket: aws.String(bucketName)})
		if err != nil && !isS3NotFound(err) {
			return err
		}
		return nil
	}

	awsTags := make([]s3types.Tag, 0, len(tags))
	for k, v := range tags {
		awsTags = append(awsTags, s3types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}

	_, err := s3c.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{
		Bucket: aws.String(bucketName),
		Tagging: &s3types.Tagging{
			TagSet: awsTags,
		},
	})
	return err
}

func ensureBucketDeleted(ctx context.Context, s3c *s3.Client, bucketName string, forceDelete bool) error {
	_, err := s3c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucketName)})
	if err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return err
	}

	if forceDelete {
		if err := emptyBucket(ctx, s3c, bucketName); err != nil {
			return err
		}
	}

	_, err = s3c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucketName)})
	if err != nil && isS3NotFound(err) {
		return nil
	}
	return err
}

func emptyBucket(ctx context.Context, s3c *s3.Client, bucketName string) error {
	p := s3.NewListObjectVersionsPaginator(s3c, &s3.ListObjectVersionsInput{
		Bucket: aws.String(bucketName),
	})

	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}

		objects := make([]s3types.ObjectIdentifier, 0, len(page.Versions)+len(page.DeleteMarkers))
		for _, v := range page.Versions {
			objects = append(objects, s3types.ObjectIdentifier{Key: v.Key, VersionId: v.VersionId})
		}
		for _, m := range page.DeleteMarkers {
			objects = append(objects, s3types.ObjectIdentifier{Key: m.Key, VersionId: m.VersionId})
		}

		if len(objects) == 0 {
			continue
		}

		_, err = s3c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucketName),
			Delete: &s3types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func isS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchBucket":
			return true
		}
	}

	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		if respErr.HTTPStatusCode() == 404 {
			return true
		}
	}

	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *S3BucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&awsv1alpha1.S3Bucket{}).
		Named("s3bucket").
		Complete(r)
}
