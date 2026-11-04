// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"flag"
	"fmt"
	api "github.com/floriankesslergmbh/bartl-forgejo/api/v1alpha1"
	"github.com/floriankesslergmbh/bartl-forgejo/internal/controller"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"time"
)

func main() {
	var metrics, health string
	var leader, request bool
	flag.StringVar(&metrics, "metrics-bind-address", ":8080", "Metrics address")
	flag.StringVar(&health, "health-probe-bind-address", ":8081", "Health address")
	flag.BoolVar(&leader, "leader-elect", true, "Elect one lifecycle owner")
	flag.BoolVar(&request, "request-backup", false, "Request scheduled capture through the application controller")
	z := zap.Options{}
	z.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&z)))
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		must(fmt.Errorf("POD_NAMESPACE is required"))
	}
	scheme := runtime.NewScheme()
	must(clientgoscheme.AddToScheme(scheme))
	must(api.AddToScheme(scheme))
	cfg := ctrl.GetConfigOrDie()
	if request {
		c, e := client.New(cfg, client.Options{Scheme: scheme})
		must(e)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		must(requestBackup(ctx, c, ns, time.Now()))
		return
	}
	image := os.Getenv("WORKER_IMAGE")
	if image == "" {
		must(fmt.Errorf("WORKER_IMAGE is required"))
	}
	mgr, e := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme, Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}}, Metrics: metricsserver.Options{BindAddress: metrics}, HealthProbeBindAddress: health, LeaderElection: leader, LeaderElectionID: "forgejo.bartl.sh", LeaderElectionNamespace: ns})
	must(e)
	r := &controller.Reconciler{Client: mgr.GetClient(), Reader: mgr.GetAPIReader(), Scheme: scheme, Namespace: ns, Image: image}
	must(r.SetupWithManager(mgr))
	must(mgr.AddHealthzCheck("healthz", healthz.Ping))
	must(mgr.AddReadyzCheck("readyz", healthz.Ping))
	must(mgr.Start(ctrl.SetupSignalHandler()))
}
func requestBackup(ctx context.Context, c client.Client, namespace string, now time.Time) error {
	a := &api.ForgejoApplication{}
	if e := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "forgejo"}, a); e != nil {
		return e
	}
	if !a.DeletionTimestamp.IsZero() || a.Spec.Replicas == 0 {
		return nil
	}
	// Do not replace an unhandled request; scheduling never drops a pending backup.
	if q := a.Annotations[controller.BackupRequest]; q != "" && q != a.Status.LastHandledBackupRequest {
		return nil
	}
	old := a.DeepCopy()
	if a.Annotations == nil {
		a.Annotations = map[string]string{}
	}
	a.Annotations[controller.BackupRequest] = "scheduled-" + now.UTC().Format("20060102T150405.000000000Z")
	// A concurrent manual request wins; the next scheduled run can try again.
	e := c.Patch(ctx, a, client.MergeFromWithOptions(old, client.MergeFromWithOptimisticLock{}))
	if apierrors.IsConflict(e) {
		return nil
	}
	return e
}
func must(e error) {
	if e != nil {
		ctrl.Log.Error(e, "Controller cannot continue")
		os.Exit(1)
	}
}
