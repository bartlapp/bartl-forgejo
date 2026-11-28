// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"encoding/json"
	"testing"

	api "github.com/floriankesslergmbh/bartl-forgejo/api/v1alpha2"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func completion(t *testing.T, a *api.ForgejoApplication, success, intact bool) (*batchv1.Job, *corev1.Pod) {
	t.Helper()
	o := a.Status.Operation
	o.JobCreated, o.JobUID = true, "job-uid"
	j := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName(o), Namespace: a.Namespace, UID: types.UID(o.JobUID), OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(a, api.GroupVersion.WithKind("ForgejoApplication"))}}}
	out := workerResult{Operation: o.ID, Installation: a.Spec.Identity.Installation, Instance: a.Spec.Identity.Instance, Command: o.Kind, Version: o.Version, Success: success, SourceIntact: intact, Capture: o.ID, Snapshot: "published-snapshot", Message: "native result"}
	message, e := json.Marshal(out)
	if e != nil {
		t.Fatal(e)
	}
	code := int32(0)
	if !success {
		code = 1
	}
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "completion", Namespace: a.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(j, batchv1.SchemeGroupVersion.WithKind("Job"))}}, Status: corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{{Name: "forgejo", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: code, Message: string(message)}}}}}}
	return j, p
}

func requireNoWriters(t *testing.T, r *Reconciler, a *api.ForgejoApplication) {
	t.Helper()
	d := &appsv1.Deployment{}
	if e := r.Get(context.Background(), client.ObjectKeyFromObject(a), d); e != nil {
		t.Fatal(e)
	}
	if d.Spec.Replicas == nil || *d.Spec.Replicas != 0 {
		t.Fatal("stopped intent reactivated writers")
	}
}

func TestAtomicFinalCaptureAndStoppedPublicationRetry(t *testing.T) {
	a := application()
	a.Spec.Running = false
	a.Status.Operation = nil
	a.Annotations[BackupRequest] = "final-request"
	r := controllerFixture(t, a, true)
	a = reconcileApplication(t, r, a)
	if a.Status.Operation == nil || a.Status.Operation.Kind != "capture" || a.Status.Operation.Request != "final-request" {
		t.Fatalf("atomic stop+request did not admit capture: %+v", a.Status.Operation)
	}
	requireNoWriters(t, r, a)
	a = reconcileApplication(t, r, a)
	if a.Status.Operation.Phase != "Capturing" {
		t.Fatalf("not capturing: %+v", a.Status.Operation)
	}
	j, p := completion(t, a, true, true)
	if e := r.Status().Update(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	for _, obj := range []client.Object{j, p} {
		if e := r.Create(context.Background(), obj); e != nil {
			t.Fatal(e)
		}
	}
	a = reconcileApplication(t, r, a)
	if a.Status.Operation.Kind != "publish" {
		t.Fatalf("capture result not persisted: %+v", a.Status.Operation)
	}
	requireNoWriters(t, r, a)
	a = reconcileApplication(t, r, a)
	requireNoWriters(t, r, a)
	// Publication failure retains the capture identity; a new annotation retries
	// publication only, without a start or another capture.
	if _, e := r.failOperation(context.Background(), a, context.DeadlineExceeded); e != nil {
		t.Fatal(e)
	}
	a.Annotations[BackupRequest] = "retry-final"
	if e := r.Update(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	a = reconcileApplication(t, r, a)
	if o := a.Status.Operation; o.Kind != "publish" || o.Phase != "Publishing" || o.Request != "retry-final" {
		t.Fatalf("stopped retry lost capture: %+v", o)
	}
	requireNoWriters(t, r, a)
}

func TestStoppedEmptyTargetDoesNotPrepareForBackup(t *testing.T) {
	a := application()
	a.Spec.Running = false
	a.Status.PreparedClaim, a.Status.PreparedVersion, a.Status.Operation = "", "", nil
	r := controllerFixture(t, a, false)
	a = reconcileApplication(t, r, a)
	if a.Status.Operation != nil {
		t.Fatalf("stop initialized solely for backup: %+v", a.Status.Operation)
	}
	requireNoWriters(t, r, a)
}

func TestCompletedPreparationIsConsumedBeforeStopDrain(t *testing.T) {
	a := application()
	a.Spec.Running = false
	a.Status.PreparedClaim, a.Status.PreparedVersion = "", ""
	a.Annotations = nil
	a.Status.Operation.Kind, a.Status.Operation.Phase = "prepare", "Preparing"
	j, p := completion(t, a, true, false)
	r := controllerFixture(t, a, false, j, p)
	a = reconcileApplication(t, r, a)
	if a.Status.PreparedClaim != a.Spec.Claim || a.Status.Operation.Phase != "Succeeded" {
		t.Fatalf("valid completed preparation discarded: %+v", a.Status)
	}
	if e := r.Get(context.Background(), client.ObjectKeyFromObject(p), &corev1.Pod{}); e != nil {
		t.Fatalf("evidence deleted before persisted result: %v", e)
	}
	a = reconcileApplication(t, r, a)
	requireNoWriters(t, r, a)
	// Static edits and a controller reconciliation preserve durable stop.
	a.Spec.Ingress.Host = "changed.example.org"
	a.Generation++
	if e := r.Update(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	a = reconcileApplication(t, r, a)
	requireNoWriters(t, r, a)
}

func TestIncompletePreparationRemainsIsolatedAfterStop(t *testing.T) {
	a := application()
	a.Spec.Running = false
	a.Status.PreparedClaim, a.Status.PreparedVersion = "", ""
	a.Annotations = nil
	a.Status.Operation.Kind, a.Status.Operation.Phase = "prepare", "Preparing"
	r := controllerFixture(t, a, false)
	a = reconcileApplication(t, r, a)
	if a.Status.Operation.Phase != "Failed" || a.Status.PreparedClaim != "" {
		t.Fatalf("incomplete mutation admitted: %+v", a.Status)
	}
	a.Spec.Running = true
	a.Generation++
	if e := r.Update(context.Background(), a); e != nil {
		t.Fatal(e)
	}
	a = reconcileApplication(t, r, a)
	if c := meta.FindStatusCondition(a.Status.Conditions, "Failed"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatal("incomplete target resumed")
	}
	requireNoWriters(t, r, a)
}

func TestCaptureFailureResumptionDependsOnBoundIntactResult(t *testing.T) {
	for _, intact := range []bool{false, true} {
		for _, running := range []bool{false, true} {
			t.Run(stringBool(intact)+"/"+stringBool(running), func(t *testing.T) {
				a := application()
				a.Spec.Running = running
				a.Annotations = nil
				a.Status.Operation.Kind, a.Status.Operation.Phase = "capture", "Capturing"
				j, p := completion(t, a, false, intact)
				r := controllerFixture(t, a, false, j, p)
				if e := r.deployment(context.Background(), a, 0); e != nil {
					t.Fatal(e)
				}
				a = reconcileApplication(t, r, a)
				if a.Status.Operation.SourceIntact != intact || a.Status.Operation.Phase != "Failed" {
					t.Fatalf("capture failure result lost: %+v", a.Status.Operation)
				}
				a = reconcileApplication(t, r, a)
				if !running || !intact {
					requireNoWriters(t, r, a)
				} else {
					d := &appsv1.Deployment{}
					if e := r.Get(context.Background(), client.ObjectKeyFromObject(a), d); e != nil || *d.Spec.Replicas != 1 {
						t.Fatalf("intact running source did not resume: %v", e)
					}
				}
			})
		}
	}
}

func stringBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestStaleCompletionCannotPrepareOrOverrideStop(t *testing.T) {
	a := application()
	a.Spec.Running = false
	a.Status.PreparedClaim, a.Status.PreparedVersion = "", ""
	a.Annotations = nil
	a.Status.Operation.Kind, a.Status.Operation.Phase = "prepare", "Preparing"
	j, p := completion(t, a, true, false)
	j.UID = "different-job"
	r := controllerFixture(t, a, false, j, p)
	a = reconcileApplication(t, r, a)
	if a.Status.PreparedClaim != "" || a.Status.Operation.Phase != "Failed" {
		t.Fatalf("stale result authorized preparation: %+v", a.Status)
	}
	a = reconcileApplication(t, r, a)
	requireNoWriters(t, r, a)
}

func TestOrdinaryCaptureResumesRunningSourceForPublication(t *testing.T) {
	a := application()
	a.Status.Operation.Kind, a.Status.Operation.Phase = "capture", "Capturing"
	j, p := completion(t, a, true, true)
	r := controllerFixture(t, a, false, j, p)
	if e := r.deployment(context.Background(), a, 0); e != nil {
		t.Fatal(e)
	}
	a = reconcileApplication(t, r, a)
	a = reconcileApplication(t, r, a)
	d := &appsv1.Deployment{}
	if e := r.Get(context.Background(), client.ObjectKeyFromObject(a), d); e != nil || *d.Spec.Replicas != 1 {
		t.Fatalf("ordinary backup did not resume source: %v", e)
	}
}

func TestCompletedOldTargetNeverPreparesEditedTarget(t *testing.T) {
	a := application()
	a.Spec.Running = false
	a.Status.PreparedClaim, a.Status.PreparedVersion = "", ""
	a.Annotations = nil
	a.Status.Operation.Kind, a.Status.Operation.Phase = "prepare", "Preparing"
	j, p := completion(t, a, true, false)
	a.Spec.Claim = "new-target"
	a.Generation++
	r := controllerFixture(t, a, false, j, p)
	a = reconcileApplication(t, r, a)
	if a.Status.PreparedClaim != "state" || a.Status.PreparedClaim == a.Spec.Claim {
		t.Fatalf("old completion authorized edited target: %+v", a.Status)
	}
	if e := r.Get(context.Background(), client.ObjectKeyFromObject(p), &corev1.Pod{}); e != nil {
		t.Fatalf("bound old-target evidence deleted before persistence: %v", e)
	}
}

func TestCompletedJobAdmissionAcknowledgementIsPersistedBeforeStop(t *testing.T) {
	a := application()
	a.Spec.Running = false
	a.Status.PreparedClaim, a.Status.PreparedVersion = "", ""
	a.Annotations = nil
	a.Status.Operation.Kind, a.Status.Operation.Phase = "prepare", "Preparing"
	j, p := completion(t, a, true, false)
	// The API accepted Job creation but the controller lost its status write.
	a.Status.Operation.JobCreated, a.Status.Operation.JobUID = false, ""
	r := controllerFixture(t, a, false, j, p)
	a = reconcileApplication(t, r, a)
	if !a.Status.Operation.JobCreated || a.Status.Operation.JobUID != string(j.UID) {
		t.Fatal("existing operation Job admission was discarded")
	}
	a = reconcileApplication(t, r, a)
	if a.Status.PreparedClaim != "state" || a.Status.Operation.Phase != "Succeeded" {
		t.Fatalf("bound native success was discarded after lost acknowledgement: %+v", a.Status)
	}
}

type finiteObservationDenied struct {
	client.Reader
	jobs bool
}

func (r finiteObservationDenied) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, job := obj.(*batchv1.Job); job && r.jobs {
		return context.DeadlineExceeded
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func (r finiteObservationDenied) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, pods := list.(*corev1.PodList); pods && !r.jobs {
		return context.DeadlineExceeded
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestStopWithdrawsServingDuringFiniteObservationFailure(t *testing.T) {
	for _, jobs := range []bool{true, false} {
		t.Run(stringBool(jobs), func(t *testing.T) {
			a := application()
			a.Spec.Running = false
			a.Status.Operation.Phase = "Publishing"
			a.Annotations[BackupRequest] = a.Status.Operation.Request
			j, p := completion(t, a, true, false)
			cron := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "forgejo-backup", Namespace: a.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(a, api.GroupVersion.WithKind("ForgejoApplication"))}}}
			r := controllerFixture(t, a, true, j, p, cron)
			r.Reader = finiteObservationDenied{Reader: r.Client, jobs: jobs}
			if _, e := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); e == nil {
				t.Fatal("failed observation was not reported")
			}
			requireNoWriters(t, r, a)
			for _, obj := range []client.Object{j, p} {
				if e := r.Get(context.Background(), client.ObjectKeyFromObject(obj), obj); e != nil {
					t.Fatalf("unconsumed completion evidence removed: %v", e)
				}
			}
			if e := r.Get(context.Background(), client.ObjectKeyFromObject(cron), &batchv1.CronJob{}); e == nil {
				t.Fatal("scheduled admission remained enabled during stop")
			}
			r.Reader = r.Client
			a = reconcileApplication(t, r, a)
			if a.Status.Operation.Phase != "Succeeded" || a.Status.Backup == nil || !a.Status.Backup.Success || a.Status.PreparedClaim != "state" {
				t.Fatalf("restored observation did not consume retained result: %+v", a.Status)
			}
			requireNoWriters(t, r, a)
			a = reconcileApplication(t, r, a)
			requireNoWriters(t, r, a)
		})
	}
}

func TestIntactFailedCaptureAcceptsFreshStagingWithoutSourcePreparation(t *testing.T) {
	a := application()
	a.Spec.Running = false
	a.Spec.StagingClaim = "fresh-staging"
	a.Status.Operation.Kind = "capture"
	a.Status.Operation.SourceIntact = true
	fresh := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "fresh-staging", Namespace: a.Namespace}}
	r := controllerFixture(t, a, false, fresh)
	a = reconcileApplication(t, r, a)
	o := a.Status.Operation
	if o.Kind != "capture" || o.Phase != "Draining" || o.StagingClaim != "fresh-staging" || o.Claim != "state" || o.ID == "capture-id" {
		t.Fatalf("fresh staging did not create independent capture: %+v", o)
	}
	if a.Status.PreparedClaim != "state" || a.Status.PreparedVersion != a.Spec.Version {
		t.Fatal("fresh staging invalidated intact native source")
	}
	a = reconcileApplication(t, r, a)
	if a.Status.Operation.Phase != "Capturing" {
		t.Fatalf("fresh staging required source reconstruction: %+v", a.Status.Operation)
	}
	requireNoWriters(t, r, a)
}
