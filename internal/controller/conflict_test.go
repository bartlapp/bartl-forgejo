// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	api "github.com/floriankesslergmbh/bartl-forgejo/api/v1alpha2"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type deploymentConflictClient struct {
	client.Client
	conflicted bool
}

func (c *deploymentConflictClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if _, ok := obj.(*appsv1.Deployment); ok && !c.conflicted {
		c.conflicted = true
		return apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, obj.GetName(), fmt.Errorf("object has been modified"))
	}
	return c.Client.Update(ctx, obj, opts...)
}

func TestDeploymentConflictRequeuesWithoutLifecycleFailure(t *testing.T) {
	for _, phase := range []string{"Succeeded", "Draining"} {
		t.Run(phase, func(t *testing.T) {
			a := application()
			a.Annotations = nil
			a.Status.Operation.Phase = phase
			a.Status.Operation.Generation = a.Generation
			if phase == "Draining" {
				a.Status.Operation.Kind = "capture"
			}
			condition(a, "Ready", metav1.ConditionTrue, "NativeReady", "Native service is healthy")
			r := controllerFixture(t, a, true)
			base := r.Client
			r.Client = &deploymentConflictClient{Client: base}
			key := client.ObjectKeyFromObject(a)
			before := &api.ForgejoApplication{}
			if e := base.Get(context.Background(), key, before); e != nil {
				t.Fatal(e)
			}
			_, e := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
			if !apierrors.IsConflict(e) {
				t.Fatalf("expected reconciliation conflict, got %v", e)
			}
			got := &api.ForgejoApplication{}
			if e := base.Get(context.Background(), key, got); e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(got.Status, before.Status) {
				t.Fatalf("API coordination conflict changed durable lifecycle state: before=%+v after=%+v", before.Status, got.Status)
			}
			got = reconcileApplication(t, r, a)
			want := phase
			if phase == "Draining" {
				want = "Capturing"
			}
			if got.Status.Operation.Phase != want {
				t.Fatalf("fresh reconciliation did not resume: %+v", got.Status.Operation)
			}
		})
	}
}

func TestOperationConflictDoesNotConsumeRequestOrFailCapture(t *testing.T) {
	for _, kind := range []string{"capture", "publish"} {
		t.Run(kind, func(t *testing.T) {
			a := application()
			a.Status.Operation.Kind = kind
			a.Status.Operation.Phase = "Publishing"
			if kind == "capture" {
				a.Status.Operation.Phase = "Capturing"
			}
			r := controllerFixture(t, a, true)
			before := a.DeepCopy().Status
			e := apierrors.NewConflict(schema.GroupResource{Group: "batch", Resource: "jobs"}, "operation", fmt.Errorf("object has been modified"))
			if _, got := r.failOperation(context.Background(), a, e); !apierrors.IsConflict(got) {
				t.Fatalf("expected conflict, got %v", got)
			}
			if !reflect.DeepEqual(a.Status, before) {
				t.Fatalf("conflict consumed finite operation: %+v", a.Status)
			}
		})
	}
}
