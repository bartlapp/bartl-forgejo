// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	api "github.com/floriankesslergmbh/bartl-forgejo/api/v1alpha2"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func ptr[T any](v T) *T      { return &v }
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:])[:16] }
func labels(a *api.ForgejoApplication, role string) map[string]string {
	return map[string]string{"app.kubernetes.io/name": "forgejo", "forgejo.bartl.sh/owner": string(a.UID), "forgejo.bartl.sh/role": role}
}
func (r *Reconciler) owned(ctx context.Context, a *api.ForgejoApplication, o client.Object, mutate func() error) error {
	key := client.ObjectKeyFromObject(o)
	if err := r.Reader.Get(ctx, key, o); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else if !metav1.IsControlledBy(o, a) {
		return fmt.Errorf("refusing foreign %T %s", o, key)
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, o, func() error {
		if o.GetResourceVersion() != "" && !metav1.IsControlledBy(o, a) {
			return fmt.Errorf("foreign object %s", key)
		}
		if err := controllerutil.SetControllerReference(a, o, r.Scheme); err != nil {
			return err
		}
		return mutate()
	})
	return err
}
func (r *Reconciler) pod(a *api.ForgejoApplication, command string) corev1.PodSpec {
	version := a.Spec.Version
	op := ""
	if a.Status.Operation != nil && command != "serve" {
		version = a.Status.Operation.Version
		op = a.Status.Operation.ID
	}
	env := []corev1.EnvVar{{Name: "DATA_DIR", Value: "/data"}, {Name: "STAGING_DIR", Value: "/staging"}, {Name: "CONFIG_FILE", Value: "/configuration/app.ini"}, {Name: "VERSION", Value: version}, {Name: "INSTALLATION_ID", Value: a.Spec.Identity.Installation}, {Name: "INSTANCE_ID", Value: a.Spec.Identity.Instance}, {Name: "OPERATION_ID", Value: op}}
	// Native Go TLS clients (including admin auth CLI invocations via exec)
	// consume this optional input from the already mounted configuration Secret.
	if command == "prepare" || command == "serve" {
		env = append(env, corev1.EnvVar{Name: "SSL_CERT_FILE", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: a.Spec.ConfigurationSecret}, Key: "SSL_CERT_FILE", Optional: ptr(true)}}})
	}
	volumes := []corev1.Volume{{Name: "state", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: a.Spec.Claim}}}, {Name: "configuration", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: a.Spec.ConfigurationSecret}}}, {Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	mounts := []corev1.VolumeMount{{Name: "state", MountPath: "/data"}, {Name: "configuration", MountPath: "/configuration", ReadOnly: true}, {Name: "tmp", MountPath: "/tmp"}}
	if command == "publish" {
		volumes = volumes[1:]
		mounts = mounts[1:]
	}
	if command == "capture" || command == "publish" {
		volumes = append(volumes, corev1.Volume{Name: "staging", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: a.Spec.StagingClaim}}})
		mounts = append(mounts, corev1.VolumeMount{Name: "staging", MountPath: "/staging"})
	}
	if command == "capture" {
		env = append(env, corev1.EnvVar{Name: "SOURCE_STOPPED", Value: "true"})
	}
	if command == "prepare" && a.Spec.BootstrapSecret != "" {
		env = append(env, corev1.EnvVar{Name: "BOOTSTRAP_CONFIGURED", Value: "true"})
		volumes = append(volumes, corev1.Volume{Name: "bootstrap", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: a.Spec.BootstrapSecret, Optional: ptr(true)}}})
		mounts = append(mounts, corev1.VolumeMount{Name: "bootstrap", MountPath: "/bootstrap", ReadOnly: true})
	}
	if a.Spec.Backup != nil && (command == "prepare" || command == "publish") {
		secret := a.Spec.Backup.ReaderSecret
		if command == "publish" {
			secret = a.Spec.Backup.WriterSecret
		}
		volumes = append(volumes, corev1.Volume{Name: "backup", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secret, Optional: ptr(command == "prepare")}}})
		mounts = append(mounts, corev1.VolumeMount{Name: "backup", MountPath: "/backup", ReadOnly: true})
		env = append(env, corev1.EnvVar{Name: "RESTIC_REPOSITORY", Value: a.Spec.Backup.Repository}, corev1.EnvVar{Name: "RESTIC_PASSWORD_FILE", Value: "/backup/password"})
		for name, key := range map[string]string{"RESTIC_REST_USERNAME": "username", "RESTIC_REST_PASSWORD": "restPassword"} {
			env = append(env, corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: key, Optional: ptr(command == "prepare")}}})
		}
	}
	c := corev1.Container{Name: "forgejo", Image: r.Image, ImagePullPolicy: corev1.PullIfNotPresent, Command: []string{"/usr/local/bin/forgejo-worker", command}, Env: env, VolumeMounts: mounts, SecurityContext: &corev1.SecurityContext{AllowPrivilegeEscalation: ptr(false), Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}, TerminationMessagePath: "/dev/termination-log", TerminationMessagePolicy: corev1.TerminationMessageReadFile}
	if command == "serve" {
		c.Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: 3000}, {Name: "ssh", ContainerPort: 2222}}
		c.ReadinessProbe = &corev1.Probe{ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/api/healthz", Port: intstr.FromInt32(3000)}}, PeriodSeconds: 3, FailureThreshold: 3}
		c.StartupProbe = &corev1.Probe{ProbeHandler: c.ReadinessProbe.ProbeHandler, PeriodSeconds: 3, FailureThreshold: 100}
	}
	return corev1.PodSpec{ServiceAccountName: "forgejo-worker", AutomountServiceAccountToken: ptr(false), RestartPolicy: corev1.RestartPolicyNever, TerminationGracePeriodSeconds: ptr(int64(120)), SecurityContext: &corev1.PodSecurityContext{RunAsUser: ptr(int64(1000)), RunAsGroup: ptr(int64(1000)), RunAsNonRoot: ptr(true), FSGroup: ptr(int64(1000)), SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}, Containers: []corev1.Container{c}, Volumes: volumes}
}
func (r *Reconciler) deployment(ctx context.Context, a *api.ForgejoApplication, n int32) error {
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "forgejo", Namespace: a.Namespace}}
	return r.owned(ctx, a, d, func() error {
		p := r.pod(a, "serve")
		p.RestartPolicy = corev1.RestartPolicyAlways
		d.Spec = appsv1.DeploymentSpec{Replicas: &n, Selector: &metav1.LabelSelector{MatchLabels: labels(a, "app")}, Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels(a, "app")}, Spec: p}}
		return nil
	})
}
func (r *Reconciler) network(ctx context.Context, a *api.ForgejoApplication) error {
	// Finite work has no ingress. Capture also has no egress; prepare and
	// publish need repository access, with outside effects disabled natively.
	for _, role := range []string{"app", "prepare", "capture", "publish"} {
		p := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "forgejo-" + role, Namespace: a.Namespace}}
		if err := r.owned(ctx, a, p, func() error {
			p.Spec = networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: labels(a, role)}, PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}}
			if role == "app" {
				p.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{Ports: []networkingv1.NetworkPolicyPort{{Protocol: ptr(corev1.ProtocolTCP), Port: ptr(intstr.FromInt32(3000))}, {Protocol: ptr(corev1.ProtocolTCP), Port: ptr(intstr.FromInt32(2222))}}}}
				p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{}}
			}
			// Restores need repository access. A separate native configuration profile
			// disables external effects; ingress remains denied during preparation.
			if role == "prepare" || role == "publish" {
				p.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{}}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "forgejo", Namespace: a.Namespace}}
	if err := r.owned(ctx, a, s, func() error {
		s.Spec.Selector = labels(a, "app")
		s.Spec.PublishNotReadyAddresses = false
		s.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: 3000, TargetPort: intstr.FromInt32(3000)}, {Name: "ssh", Port: sshPort(a), TargetPort: intstr.FromInt32(2222)}}
		return nil
	}); err != nil {
		return err
	}
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "forgejo", Namespace: a.Namespace}}
	if !a.Spec.Ingress.Enabled {
		return r.deleteOwned(ctx, a, ing)
	}
	return r.owned(ctx, a, ing, func() error {
		t := networkingv1.PathTypePrefix
		ing.Spec = networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: a.Spec.Ingress.Host, IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{Path: "/", PathType: &t, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "forgejo", Port: networkingv1.ServiceBackendPort{Name: "http"}}}}}}}}}}
		if a.Spec.Ingress.ClassName != "" {
			ing.Spec.IngressClassName = ptr(a.Spec.Ingress.ClassName)
		}
		if a.Spec.Ingress.TLSSecret != "" {
			ing.Spec.TLS = []networkingv1.IngressTLS{{Hosts: []string{a.Spec.Ingress.Host}, SecretName: a.Spec.Ingress.TLSSecret}}
		}
		return nil
	})
}
func sshPort(a *api.ForgejoApplication) int32 {
	if a.Spec.SSH.Port == 0 {
		return 2222
	}
	return a.Spec.SSH.Port
}
func (r *Reconciler) deleteOwned(ctx context.Context, a *api.ForgejoApplication, o client.Object) error {
	if err := r.Reader.Get(ctx, client.ObjectKeyFromObject(o), o); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !metav1.IsControlledBy(o, a) {
		return fmt.Errorf("refusing foreign %T %s", o, o.GetName())
	}
	if !o.GetDeletionTimestamp().IsZero() {
		return nil
	}
	return r.Delete(ctx, o, client.PropagationPolicy(metav1.DeletePropagationForeground))
}
func jobName(o *api.Operation) string {
	return "forgejo-" + o.Kind + "-" + digest(o.ID+o.Phase+o.Request)
}
func (r *Reconciler) job(ctx context.Context, a *api.ForgejoApplication, command string) (*batchv1.Job, error) {
	o := a.Status.Operation
	j := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName(o), Namespace: a.Namespace}}
	err := r.Reader.Get(ctx, client.ObjectKeyFromObject(j), j)
	if err == nil {
		if !metav1.IsControlledBy(j, a) {
			return nil, fmt.Errorf("refusing foreign Job %s", j.Name)
		}
		if o.JobCreated && string(j.UID) != o.JobUID {
			return nil, fmt.Errorf("operation Job identity changed; refusing stale or replayed completion")
		}
		return j, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, observationError{err}
	}
	if o.JobCreated {
		return nil, fmt.Errorf("operation Job disappeared; refusing blind replay")
	}
	p := r.pod(a, command)
	j.Labels = labels(a, command)
	j.Spec = batchv1.JobSpec{BackoffLimit: ptr(int32(0)), Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: labels(a, command), Annotations: map[string]string{"forgejo.bartl.sh/operation": o.ID}}, Spec: p}}
	if err = controllerutil.SetControllerReference(a, j, r.Scheme); err != nil {
		return nil, err
	}
	if err = r.Create(ctx, j); err != nil {
		return nil, observationError{err}
	}
	return j, nil
}
func (r *Reconciler) schedule(ctx context.Context, a *api.ForgejoApplication) error {
	c := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Name: "forgejo-backup", Namespace: a.Namespace}}
	if a.Spec.Backup == nil || a.Spec.Backup.Schedule == "" {
		return r.deleteOwned(ctx, a, c)
	}
	return r.owned(ctx, a, c, func() error {
		p := corev1.PodSpec{ServiceAccountName: "forgejo-controller", RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "request", Image: r.Image, Command: []string{"/usr/local/bin/controller", "--request-backup"}, Env: []corev1.EnvVar{{Name: "POD_NAMESPACE", Value: a.Namespace}}, SecurityContext: &corev1.SecurityContext{RunAsNonRoot: ptr(true), RunAsUser: ptr(int64(1000)), AllowPrivilegeEscalation: ptr(false)}}}}
		c.Spec = batchv1.CronJobSpec{Schedule: a.Spec.Backup.Schedule, Suspend: ptr(!a.Spec.Running), ConcurrencyPolicy: batchv1.ForbidConcurrent, StartingDeadlineSeconds: ptr(int64(300)), SuccessfulJobsHistoryLimit: ptr(int32(1)), FailedJobsHistoryLimit: ptr(int32(2)), JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{BackoffLimit: ptr(int32(0)), Template: corev1.PodTemplateSpec{Spec: p}}}}
		if a.Spec.Backup.TimeZone != "" {
			c.Spec.TimeZone = ptr(a.Spec.Backup.TimeZone)
		}
		return nil
	})
}
