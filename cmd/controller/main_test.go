// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"testing"
	"time"

	api "github.com/floriankesslergmbh/bartl-forgejo/api/v1alpha2"
	"github.com/floriankesslergmbh/bartl-forgejo/internal/controller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type concurrentRequestClient struct{ client.Client }

func (c *concurrentRequestClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	current := &api.ForgejoApplication{}
	if e := c.Get(ctx, client.ObjectKeyFromObject(obj), current); e != nil {
		return e
	}
	if current.Annotations == nil {
		current.Annotations = map[string]string{}
	}
	current.Annotations[controller.BackupRequest] = "manual-after-scheduler-read"
	if e := c.Update(ctx, current); e != nil {
		return e
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestScheduledRequestPreservesConcurrentManualRequest(t *testing.T) {
	s := runtime.NewScheme()
	if e := api.AddToScheme(s); e != nil {
		t.Fatal(e)
	}
	a := &api.ForgejoApplication{ObjectMeta: metav1.ObjectMeta{Name: "forgejo", Namespace: "test"}, Spec: api.ForgejoApplicationSpec{Running: true}}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(a).Build()
	c := &concurrentRequestClient{Client: base}
	if e := requestBackup(context.Background(), c, "test", time.Unix(100, 0)); e != nil {
		t.Fatal(e)
	}
	got := &api.ForgejoApplication{}
	if e := base.Get(context.Background(), client.ObjectKeyFromObject(a), got); e != nil {
		t.Fatal(e)
	}
	if got.Annotations[controller.BackupRequest] != "manual-after-scheduler-read" {
		t.Fatalf("manual request overwritten: %v", got.Annotations)
	}
}

func TestScheduledRequestDoesNotReplacePendingRequest(t *testing.T) {
	s := runtime.NewScheme()
	if e := api.AddToScheme(s); e != nil {
		t.Fatal(e)
	}
	a := &api.ForgejoApplication{ObjectMeta: metav1.ObjectMeta{Name: "forgejo", Namespace: "test", Annotations: map[string]string{controller.BackupRequest: "manual-pending"}}, Spec: api.ForgejoApplicationSpec{Running: true}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(a).Build()
	if e := requestBackup(context.Background(), c, "test", time.Unix(100, 0)); e != nil {
		t.Fatal(e)
	}
	got := &api.ForgejoApplication{}
	if e := c.Get(context.Background(), client.ObjectKeyFromObject(a), got); e != nil {
		t.Fatal(e)
	}
	if got.Annotations[controller.BackupRequest] != "manual-pending" {
		t.Fatalf("pending request overwritten: %v", got.Annotations)
	}
}
