// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	api "github.com/floriankesslergmbh/bartl-forgejo/api/v1alpha1"
	"github.com/robfig/cron/v3"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"time"
)

const BackupRequest = "forgejo.bartl.sh/backup-request"
const finalizer = "forgejo.bartl.sh/drain"

type Reconciler struct {
	client.Client
	Reader    client.Reader
	Scheme    *runtime.Scheme
	Namespace string
	Image     string
}
type observationError struct{ error }

func (r *Reconciler) SetupWithManager(m ctrl.Manager) error {
	changes := predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
		return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() || e.ObjectOld.GetDeletionTimestamp().IsZero() != e.ObjectNew.GetDeletionTimestamp().IsZero() || e.ObjectOld.GetAnnotations()[BackupRequest] != e.ObjectNew.GetAnnotations()[BackupRequest]
	}}
	return ctrl.NewControllerManagedBy(m).For(&api.ForgejoApplication{}, builder.WithPredicates(changes)).Owns(&batchv1.Job{}).Owns(&appsv1.Deployment{}).Complete(r)
}
func condition(a *api.ForgejoApplication, t string, s metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&a.Status.Conditions, metav1.Condition{Type: t, Status: s, Reason: reason, Message: message, ObservedGeneration: a.Generation})
}
func (r *Reconciler) save(ctx context.Context, a *api.ForgejoApplication) (ctrl.Result, error) {
	a.Status.ObservedGeneration = a.Generation
	return ctrl.Result{RequeueAfter: 3 * time.Second}, r.Status().Update(ctx, a)
}
func (r *Reconciler) fail(ctx context.Context, a *api.ForgejoApplication, e error) (ctrl.Result, error) {
	if apierrors.IsConflict(e) {
		return ctrl.Result{}, e
	}
	condition(a, "Ready", metav1.ConditionFalse, "LifecycleFailed", e.Error())
	condition(a, "Failed", metav1.ConditionTrue, "LifecycleFailed", e.Error())
	return r.save(ctx, a)
}
func validate(a *api.ForgejoApplication) error {
	if a.Name != "forgejo" {
		return fmt.Errorf("only application forgejo is supported")
	}
	if a.Spec.Version != "16.0.7" && a.Spec.Version != "17.0.1" {
		return fmt.Errorf("unsupported version %q", a.Spec.Version)
	}
	if a.Spec.Replicas != 0 && a.Spec.Replicas != 1 {
		return fmt.Errorf("replicas must be zero or one")
	}
	if a.Spec.Claim == "" || a.Spec.StagingClaim == "" || a.Spec.Claim == a.Spec.StagingClaim {
		return fmt.Errorf("distinct existing state and staging claims required")
	}
	if a.Spec.Identity.Installation == "" || a.Spec.Identity.Instance == "" || a.Spec.ConfigurationSecret == "" {
		return fmt.Errorf("identity and configurationSecret are required")
	}
	if sshPort(a) < 1 || sshPort(a) > 65535 {
		return fmt.Errorf("invalid SSH service port")
	}
	if a.Spec.Backup != nil {
		b := a.Spec.Backup
		if b.Repository == "" || b.ReaderSecret == "" || b.WriterSecret == "" {
			return fmt.Errorf("backup repository and distinct credential references required")
		}
		if b.ReaderSecret == b.WriterSecret {
			return fmt.Errorf("reader and writer secrets must differ")
		}
		if b.Schedule != "" {
			if _, e := cron.ParseStandard(b.Schedule); e != nil {
				return fmt.Errorf("invalid backup schedule: %w", e)
			}
		}
		if b.TimeZone != "" {
			if _, e := time.LoadLocation(b.TimeZone); e != nil {
				return fmt.Errorf("invalid backup timeZone: %w", e)
			}
		}
	}
	return nil
}
func (r *Reconciler) inputs(ctx context.Context, a *api.ForgejoApplication) error {
	for _, name := range []string{a.Spec.Claim, a.Spec.StagingClaim} {
		p := &corev1.PersistentVolumeClaim{}
		if e := r.Reader.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: name}, p); e != nil {
			return e
		}
		if !p.DeletionTimestamp.IsZero() {
			return fmt.Errorf("claim %s is deleting", name)
		}
	}
	s := &corev1.Secret{}
	if e := r.Reader.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: a.Spec.ConfigurationSecret}, s); e != nil {
		return e
	}
	if len(s.Data["app.ini"]) == 0 {
		return fmt.Errorf("configurationSecret lacks app.ini")
	}
	return nil
}
func newID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func (r *Reconciler) begin(ctx context.Context, a *api.ForgejoApplication, kind, request string) (ctrl.Result, error) {
	id, e := newID()
	if e != nil {
		return ctrl.Result{}, e
	}
	a.Status.Operation = &api.Operation{ID: id, Kind: kind, Phase: "Draining", Version: a.Spec.Version, Claim: a.Spec.Claim, StagingClaim: a.Spec.StagingClaim, Request: request, Generation: a.Generation}
	condition(a, "Ready", metav1.ConditionFalse, "Draining", "Waiting for predecessor application Pods to terminate")
	return r.save(ctx, a)
}
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if req.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}
	a := &api.ForgejoApplication{}
	if e := r.Reader.Get(ctx, req.NamespacedName, a); e != nil {
		return ctrl.Result{}, client.IgnoreNotFound(e)
	}
	// A fresh API read is required to admit work. Observation loss does not scale
	// down an already admitted server; it blocks all new lifecycle decisions.
	if a.Name != "forgejo" {
		return r.fail(ctx, a, fmt.Errorf("only application forgejo is supported"))
	}
	if a.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(a, finalizer) {
		controllerutil.AddFinalizer(a, finalizer)
		return ctrl.Result{Requeue: true}, r.Update(ctx, a)
	}
	if !a.DeletionTimestamp.IsZero() || a.Spec.Replicas == 0 {
		return r.stop(ctx, a)
	}
	if e := validate(a); e != nil {
		return r.fail(ctx, a, e)
	}
	// A protected capture must be resolved before changing its source or staging
	// location. In particular, do not replace its operation with preparation or
	// scale down the healthy predecessor merely because an incompatible edit was made.
	if o := a.Status.Operation; o != nil && o.Kind == "publish" && o.Phase != "Succeeded" &&
		(o.Claim != a.Spec.Claim || o.StagingClaim != a.Spec.StagingClaim || o.Version != a.Spec.Version) {
		ready, e := r.ready(ctx, a)
		if e != nil {
			return ctrl.Result{}, e
		}
		if ready {
			condition(a, "Ready", metav1.ConditionTrue, "NativeReady", "Existing service remains ready while publication is unresolved")
		} else {
			condition(a, "Ready", metav1.ConditionFalse, "Starting", "Waiting for native readiness")
		}
		condition(a, "Failed", metav1.ConditionTrue, "PendingPublication", "Restore the operation's state claim, staging claim and version, then retry publication before changing them")
		return r.save(ctx, a)
	}
	if e := r.inputs(ctx, a); e != nil {
		return r.fail(ctx, a, e)
	}
	if e := r.network(ctx, a); e != nil {
		return r.fail(ctx, a, e)
	}
	if e := r.schedule(ctx, a); e != nil {
		return r.fail(ctx, a, e)
	}
	condition(a, "Stopped", metav1.ConditionFalse, "StartRequested", "Application start is requested")
	o := a.Status.Operation
	if o != nil && o.Phase != "Succeeded" && o.Phase != "Failed" {
		// Publication consumes only the already protected capture. The target
		// guard above binds its claims and version; a restored intent or unrelated
		// generation edit must not drain the healthy application to cancel it.
		if o.Kind == "publish" {
			o.Generation = a.Generation
			return r.operation(ctx, a)
		}
		// Do not let a completion for an old target authorize a new target. A spec
		// edit interrupts finite work; independent state is required after mutation.
		if o.Generation != a.Generation {
			done, e := r.drain(ctx, a, true)
			if e != nil {
				return r.fail(ctx, a, e)
			}
			if !done {
				return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
			}
			o.Phase = "Failed"
			o.Message = "Operating intent changed during finite work; operation interrupted"
			return r.save(ctx, a)
		}
		return r.operation(ctx, a)
	}
	if o != nil && o.Phase == "Failed" && o.Kind != "publish" && o.Claim == a.Spec.Claim {
		return r.fail(ctx, a, fmt.Errorf("%s failed on this target: %s; use an independent clean claim", o.Kind, o.Message))
	}
	if a.Status.PreparedClaim != a.Spec.Claim || a.Status.PreparedVersion != a.Spec.Version {
		return r.begin(ctx, a, "prepare", "")
	}
	if e := r.deployment(ctx, a, 1); e != nil {
		return r.fail(ctx, a, e)
	}
	ready, e := r.ready(ctx, a)
	if e != nil {
		return r.fail(ctx, a, e)
	}
	if ready {
		condition(a, "Ready", metav1.ConditionTrue, "NativeReady", "Native Forgejo is ready")
		condition(a, "Failed", metav1.ConditionFalse, "Healthy", "No application lifecycle failure")
	} else {
		condition(a, "Ready", metav1.ConditionFalse, "Starting", "Waiting for native readiness")
	}
	request := a.Annotations[BackupRequest]
	if request != "" && request != a.Status.LastHandledBackupRequest && ready {
		if a.Spec.Backup == nil {
			a.Status.LastHandledBackupRequest = request
			condition(a, "BackupFailed", metav1.ConditionTrue, "NotConfigured", "Backup request rejected: no backup repository configured")
			return r.save(ctx, a)
		}
		if o != nil && o.Kind == "publish" && o.Phase == "Failed" {
			// The protected capture belongs to the old operation. Retry publication,
			// not capture; changing the request creates a distinct one-shot Job.
			o.Phase = "Publishing"
			o.Request = request
			o.Generation = a.Generation
			o.JobCreated = false
			o.JobUID = ""
			o.Message = ""
			return r.save(ctx, a)
		}
		return r.begin(ctx, a, "capture", request)
	}
	return r.save(ctx, a)
}
func (r *Reconciler) ready(ctx context.Context, a *api.ForgejoApplication) (bool, error) {
	d := &appsv1.Deployment{}
	e := r.Reader.Get(ctx, client.ObjectKey{Namespace: a.Namespace, Name: "forgejo"}, d)
	if e != nil {
		return false, e
	}
	if !metav1.IsControlledBy(d, a) {
		return false, fmt.Errorf("foreign serving Deployment")
	}
	return d.Status.ObservedGeneration >= d.Generation && d.Status.ReadyReplicas == 1 && d.Status.UpdatedReplicas == 1, nil
}

// drain waits for actual Pods, including terminating Pods, rather than trusting
// Deployment replica counts. Unknown Pods mounting either claim block mutation.
func (r *Reconciler) drain(ctx context.Context, a *api.ForgejoApplication, all bool) (bool, error) {
	if e := r.deployment(ctx, a, 0); e != nil {
		return false, e
	}
	if all {
		jobs := &batchv1.JobList{}
		if e := r.Reader.List(ctx, jobs, client.InNamespace(a.Namespace)); e != nil {
			return false, e
		}
		found := false
		for i := range jobs.Items {
			j := &jobs.Items[i]
			if metav1.IsControlledBy(j, a) {
				found = true
				if e := r.deleteOwned(ctx, a, j); e != nil {
					return false, e
				}
			}
		}
		if found {
			return false, nil
		}
	}
	pods := &corev1.PodList{}
	if e := r.Reader.List(ctx, pods, client.InNamespace(a.Namespace)); e != nil {
		return false, e
	}
	for _, p := range pods.Items {
		using := false
		for _, v := range p.Spec.Volumes {
			if v.PersistentVolumeClaim != nil && (v.PersistentVolumeClaim.ClaimName == a.Spec.Claim || v.PersistentVolumeClaim.ClaimName == a.Spec.StagingClaim) {
				using = true
			}
		}
		isApp := p.Labels["forgejo.bartl.sh/role"] == "app" && p.Labels["forgejo.bartl.sh/owner"] == string(a.UID)
		ownedPod := p.Labels["forgejo.bartl.sh/owner"] == string(a.UID)
		if !using && !isApp && !ownedPod {
			continue
		}
		if !p.DeletionTimestamp.IsZero() {
			return false, nil
		}
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if isApp {
			return false, nil
		}
		if all && p.Labels["forgejo.bartl.sh/owner"] == string(a.UID) {
			return false, nil
		}
		return false, fmt.Errorf("Pod %s still uses protected state; no mutation authorized", p.Name)
	}
	return true, nil
}
func (r *Reconciler) stop(ctx context.Context, a *api.ForgejoApplication) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(a, finalizer) {
		return ctrl.Result{}, nil
	}
	condition(a, "Stopped", metav1.ConditionFalse, "Draining", "Stop is not yet confirmed")
	// Suspend/delete the requester first. Cancel every direct operation Job even
	// when Job status has not yet reported Active; then await their actual Pods.
	c := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "forgejo-backup", Namespace: a.Namespace}}
	if e := r.deleteOwned(ctx, a, c); e != nil {
		return r.fail(ctx, a, e)
	}
	done, e := r.drain(ctx, a, true)
	if e != nil {
		return r.fail(ctx, a, e)
	}
	if !done {
		condition(a, "Ready", metav1.ConditionFalse, "Stopping", "Waiting for actual application and operation Pods to terminate")
		return r.save(ctx, a)
	}
	if e := r.Reader.Get(ctx, client.ObjectKeyFromObject(c), c); e == nil {
		condition(a, "Ready", metav1.ConditionFalse, "Stopping", "Waiting for scheduled requester Jobs and Pods to terminate")
		return r.save(ctx, a)
	} else if !apierrors.IsNotFound(e) {
		return ctrl.Result{}, e
	}
	if o := a.Status.Operation; o != nil && o.Phase != "Succeeded" && o.Phase != "Failed" {
		o.Phase = "Failed"
		o.Message = "Operation interrupted by explicit stop"
		if o.Kind == "publish" {
			a.Status.Backup = &api.BackupResult{Request: o.Request, Operation: o.ID, Success: false, Message: o.Message, CompletedAt: metav1.Now()}
			condition(a, "BackupFailed", metav1.ConditionTrue, "PublicationInterrupted", o.Message)
		}
	}
	condition(a, "Ready", metav1.ConditionFalse, "Stopped", "All application and operation Pods have terminated")
	condition(a, "Stopped", metav1.ConditionTrue, "Drained", "All application and operation Pods have terminated; claims and Secrets retained")
	if !a.DeletionTimestamp.IsZero() {
		controllerutil.RemoveFinalizer(a, finalizer)
		return ctrl.Result{}, r.Update(ctx, a)
	}
	return r.save(ctx, a)
}

type workerResult struct {
	Operation    string `json:"operation"`
	Installation string `json:"installation"`
	Instance     string `json:"instance"`
	Command      string `json:"command"`
	Success      bool   `json:"success"`
	Version      string `json:"version"`
	Message      string `json:"message"`
	Snapshot     string `json:"snapshot"`
	Capture      string `json:"capture"`
}

func (r *Reconciler) result(ctx context.Context, a *api.ForgejoApplication, j *batchv1.Job, command string) (*workerResult, error) {
	pods := &corev1.PodList{}
	if e := r.Reader.List(ctx, pods, client.InNamespace(a.Namespace)); e != nil {
		return nil, observationError{e}
	}
	for _, p := range pods.Items {
		if !metav1.IsControlledBy(&p, j) {
			continue
		}
		for _, s := range p.Status.ContainerStatuses {
			if s.Name != "forgejo" || s.State.Terminated == nil {
				continue
			}
			t := s.State.Terminated
			var out workerResult
			if e := json.Unmarshal([]byte(t.Message), &out); e != nil {
				return nil, fmt.Errorf("invalid worker completion: %w", e)
			}
			o := a.Status.Operation
			if out.Operation != o.ID || out.Installation != a.Spec.Identity.Installation || out.Instance != a.Spec.Identity.Instance || out.Command != command || out.Version != o.Version {
				return nil, fmt.Errorf("worker result identity mismatch")
			}
			if t.ExitCode != 0 || !out.Success {
				return nil, fmt.Errorf("%s failed: %s", command, out.Message)
			}
			if command == "capture" && out.Capture != o.ID {
				return nil, fmt.Errorf("capture result is not bound to current operation")
			}
			return &out, nil
		}
	}
	if j.Status.Failed > 0 {
		return nil, fmt.Errorf("worker Job failed without a valid completion")
	}
	if j.Status.Succeeded > 0 {
		return nil, fmt.Errorf("worker completion Pod disappeared; refusing unverified success")
	}
	return nil, nil
}
func (r *Reconciler) operation(ctx context.Context, a *api.ForgejoApplication) (ctrl.Result, error) {
	o := a.Status.Operation
	if o.Phase == "Draining" {
		done, e := r.drain(ctx, a, false)
		if e != nil {
			return r.fail(ctx, a, e)
		}
		if !done {
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
		if o.Kind == "prepare" {
			o.Phase = "Preparing"
		} else {
			o.Phase = "Capturing"
		}
		return r.save(ctx, a)
	}
	command := o.Kind
	if command == "publish" && a.Status.PreparedVersion == a.Spec.Version && a.Status.PreparedClaim == a.Spec.Claim {
		if e := r.deployment(ctx, a, 1); e != nil {
			return ctrl.Result{}, e
		}
		ready, e := r.ready(ctx, a)
		if e != nil {
			return ctrl.Result{}, e
		}
		if ready {
			condition(a, "Ready", metav1.ConditionTrue, "NativeReady", "Service resumed after successful consistent capture")
		} else {
			condition(a, "Ready", metav1.ConditionFalse, "Starting", "Waiting for native readiness while publishing")
		}
	}
	j, e := r.job(ctx, a, command)
	if e != nil {
		return r.failOperation(ctx, a, e)
	}
	if !o.JobCreated {
		o.JobCreated = true
		o.JobUID = string(j.UID)
		return r.save(ctx, a)
	}
	result, e := r.result(ctx, a, j, command)
	if e != nil {
		return r.failOperation(ctx, a, e)
	}
	if result == nil {
		if command == "publish" {
			return r.save(ctx, a)
		}
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	}
	o.Message = result.Message
	switch command {
	case "prepare":
		a.Status.PreparedVersion = o.Version
		a.Status.PreparedClaim = o.Claim
		o.Phase = "Succeeded"
		condition(a, "Failed", metav1.ConditionFalse, "Prepared", "Native startup procedure completed")
	case "capture":
		o.Kind = "publish"
		o.Phase = "Publishing"
		o.JobCreated = false
		o.JobUID = ""
		condition(a, "Ready", metav1.ConditionFalse, "Captured", "Consistent capture completed; resuming service while publishing")
	case "publish":
		o.Phase = "Succeeded"
		a.Status.LastHandledBackupRequest = o.Request
		a.Status.Backup = &api.BackupResult{Request: o.Request, Operation: o.ID, Snapshot: result.Snapshot, Success: true, Message: result.Message, CompletedAt: metav1.Now()}
		condition(a, "BackupFailed", metav1.ConditionFalse, "Published", "Complete recovery point verified in independent repository")
	default:
		return r.failOperation(ctx, a, fmt.Errorf("unknown operation kind %q", command))
	}
	return r.save(ctx, a)
}
func (r *Reconciler) failOperation(ctx context.Context, a *api.ForgejoApplication, e error) (ctrl.Result, error) {
	if apierrors.IsConflict(e) {
		return ctrl.Result{}, e
	}
	if _, unavailable := e.(observationError); unavailable {
		return ctrl.Result{}, e
	}
	o := a.Status.Operation
	o.Phase = "Failed"
	o.Message = e.Error()
	if o.Kind == "capture" || o.Kind == "publish" {
		a.Status.LastHandledBackupRequest = o.Request
		a.Status.Backup = &api.BackupResult{Request: o.Request, Operation: o.ID, Success: false, Message: e.Error(), CompletedAt: metav1.Now()}
		condition(a, "BackupFailed", metav1.ConditionTrue, "BackupFailed", e.Error())
		if o.Kind == "publish" {
			return r.save(ctx, a)
		}
	}
	return r.fail(ctx, a, e)
}
