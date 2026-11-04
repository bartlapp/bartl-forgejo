// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPublicationFailureRetainsCaptureAndReadyService(t *testing.T) {
	a := application()
	a.Status.Operation.Phase = "Publishing"
	a.Status.Operation.Request = "retry"
	condition(a, "Ready", metav1.ConditionTrue, "NativeReady", "Service resumed")
	r := controllerFixture(t, a, true)
	if _, e := r.failOperation(context.Background(), a, fmt.Errorf("repository unavailable")); e != nil {
		t.Fatal(e)
	}
	o := a.Status.Operation
	if o.Kind != "publish" || o.Phase != "Failed" || o.ID != "capture-id" || o.Claim != "state" || o.StagingClaim != "staging" || o.Version != "17.0.1" {
		t.Fatalf("lost retry identity: %+v", o)
	}
	if a.Status.LastHandledBackupRequest != "retry" || a.Status.Backup == nil || a.Status.Backup.Success {
		t.Fatalf("incorrect failed request result: %+v", a.Status.Backup)
	}
	if c := meta.FindStatusCondition(a.Status.Conditions, "Ready"); c == nil || c.Status != metav1.ConditionTrue {
		t.Fatalf("publication failure incorrectly stopped service: %+v", c)
	}
}
