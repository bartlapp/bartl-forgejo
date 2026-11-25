// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"testing"

	api "github.com/floriankesslergmbh/bartl-forgejo/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func application() *api.ForgejoApplication {
	return &api.ForgejoApplication{
		ObjectMeta: metav1.ObjectMeta{Name: "forgejo", Namespace: "test", UID: "application", Generation: 2, Finalizers: []string{finalizer}, Annotations: map[string]string{BackupRequest: "retry"}},
		Spec:       api.ForgejoApplicationSpec{Identity: api.Identity{Installation: "installation", Instance: "instance"}, Version: "17.0.1", Replicas: 1, Claim: "state", StagingClaim: "staging", ConfigurationSecret: "configuration", Backup: &api.BackupSpec{Repository: "rest:https://backup", ReaderSecret: "reader", WriterSecret: "writer"}},
		Status:     api.ForgejoApplicationStatus{PreparedClaim: "state", PreparedVersion: "17.0.1", LastHandledBackupRequest: "original", Operation: &api.Operation{ID: "capture-id", Kind: "publish", Phase: "Failed", Claim: "state", StagingClaim: "staging", Version: "17.0.1", Request: "original", Generation: 1}},
	}
}

func controllerFixture(t *testing.T, a *api.ForgejoApplication, ready bool, objects ...client.Object) *Reconciler {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{api.AddToScheme, corev1.AddToScheme, appsv1.AddToScheme, batchv1.AddToScheme, networkingv1.AddToScheme} {
		if e := add(s); e != nil {
			t.Fatal(e)
		}
	}
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "forgejo", Namespace: a.Namespace, Generation: 1, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(a, api.GroupVersion.WithKind("ForgejoApplication"))}}, Spec: appsv1.DeploymentSpec{Replicas: ptr(int32(1))}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1}}
	if ready {
		d.Status.ReadyReplicas = 1
		d.Status.UpdatedReplicas = 1
	}
	objects = append(objects, a, d,
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "state", Namespace: a.Namespace}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "staging", Namespace: a.Namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "configuration", Namespace: a.Namespace}, Data: map[string][]byte{"app.ini": []byte("configuration")}})
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&api.ForgejoApplication{}, &appsv1.Deployment{}).WithObjects(objects...).Build()
	return &Reconciler{Client: c, Reader: c, Scheme: s, Namespace: a.Namespace, Image: "worker"}
}

func reconcileApplication(t *testing.T, r *Reconciler, a *api.ForgejoApplication) *api.ForgejoApplication {
	t.Helper()
	key := client.ObjectKeyFromObject(a)
	if _, e := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); e != nil {
		t.Fatal(e)
	}
	got := &api.ForgejoApplication{}
	if e := r.Get(context.Background(), key, got); e != nil {
		t.Fatal(e)
	}
	return got
}

func TestPendingPublicationRejectsTargetChangesWithoutStoppingService(t *testing.T) {
	for _, field := range []string{"state", "staging", "version"} {
		t.Run(field, func(t *testing.T) {
			a := application()
			switch field {
			case "state":
				a.Spec.Claim = "other-state"
			case "staging":
				a.Spec.StagingClaim = "other-staging"
			case "version":
				a.Spec.Version = "16.0.7"
			}
			r := controllerFixture(t, a, true)
			got := reconcileApplication(t, r, a)
			o := got.Status.Operation
			if o.ID != "capture-id" || o.Kind != "publish" || o.Phase != "Failed" || o.Claim != "state" || o.StagingClaim != "staging" || o.Version != "17.0.1" {
				t.Fatalf("pending capture replaced: %+v", o)
			}
			if c := meta.FindStatusCondition(got.Status.Conditions, "Failed"); c == nil || c.Reason != "PendingPublication" {
				t.Fatalf("missing actionable rejection: %+v", c)
			}
			d := &appsv1.Deployment{}
			if e := r.Get(context.Background(), types.NamespacedName{Namespace: a.Namespace, Name: "forgejo"}, d); e != nil {
				t.Fatal(e)
			}
			if *d.Spec.Replicas != 1 {
				t.Fatal("healthy predecessor was stopped")
			}
		})
	}
}

func TestFailedPublicationRetriesOriginalCaptureAfterIntentRestored(t *testing.T) {
	a := application()
	a.Spec.StagingClaim = "other-staging"
	r := controllerFixture(t, a, true)
	got := reconcileApplication(t, r, a)
	got.Spec.StagingClaim = "staging"
	got.Generation++
	if e := r.Update(context.Background(), got); e != nil {
		t.Fatal(e)
	}
	got = reconcileApplication(t, r, got)
	o := got.Status.Operation
	if o.ID != "capture-id" || o.Kind != "publish" || o.Phase != "Publishing" || o.StagingClaim != "staging" || o.Version != "17.0.1" || o.Request != "retry" || o.Generation != got.Generation {
		t.Fatalf("incorrect retry transition: %+v", o)
	}
	if o.JobCreated || o.JobUID != "" {
		t.Fatal("retry retained old Job identity")
	}
}

func TestPublicationClearsStaleReady(t *testing.T) {
	a := application()
	a.Status.Operation.Phase = "Publishing"
	condition(a, "Ready", metav1.ConditionTrue, "NativeReady", "Previously ready")
	r := controllerFixture(t, a, false)
	got := reconcileApplication(t, r, a)
	if c := meta.FindStatusCondition(got.Status.Conditions, "Ready"); c == nil || c.Status != metav1.ConditionFalse {
		t.Fatalf("stale Ready condition: %+v", c)
	}
	if got.Status.Operation.Phase != "Publishing" {
		t.Fatalf("publication interrupted: %+v", got.Status.Operation)
	}
	if got.Status.Operation.Generation != got.Generation {
		t.Fatal("publication did not accept restored intent generation")
	}
}

func TestOptionalNativeTrustUsesConfigurationSecret(t *testing.T) {
	a := application()
	r := &Reconciler{Image: "candidate"}
	for _, command := range []string{"prepare", "serve", "capture", "publish"} {
		pod := r.pod(a, command)
		container := pod.Containers[0]
		found := false
		for _, env := range container.Env {
			if env.Name != "SSL_CERT_FILE" {
				continue
			}
			found = true
			ref := env.ValueFrom.SecretKeyRef
			if ref.Name != a.Spec.ConfigurationSecret || ref.Key != "SSL_CERT_FILE" || ref.Optional == nil || !*ref.Optional {
				t.Fatalf("%s has incorrect optional native CA reference: %+v", command, ref)
			}
		}
		if found != (command == "prepare" || command == "serve") {
			t.Fatalf("%s has incorrect native client trust boundary: present=%v", command, found)
		}
		mounted := false
		for _, mount := range container.VolumeMounts {
			if mount.Name == "configuration" && mount.MountPath == "/configuration" && mount.ReadOnly {
				mounted = true
			}
		}
		if !mounted {
			t.Fatalf("%s lost existing read-only configuration mount", command)
		}
	}
}
