// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "forgejo.bartl.sh", Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &ForgejoApplication{}, &ForgejoApplicationList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
})
var AddToScheme = SchemeBuilder.AddToScheme

type Identity struct {
	Installation string `json:"installation"`
	Instance     string `json:"instance"`
}
type BackupSpec struct {
	Repository   string `json:"repository"`
	ReaderSecret string `json:"readerSecret"`
	WriterSecret string `json:"writerSecret"`
	Schedule     string `json:"schedule,omitempty"`
	TimeZone     string `json:"timeZone,omitempty"`
}
type IngressSpec struct {
	Host      string `json:"host"`
	ClassName string `json:"className,omitempty"`
	TLSSecret string `json:"tlsSecret,omitempty"`
	Enabled   bool   `json:"enabled"`
}
type SSHSpec struct {
	Port int32 `json:"port,omitempty"`
}
type ForgejoApplicationSpec struct {
	Identity            Identity    `json:"identity"`
	Version             string      `json:"version"`
	Replicas            int32       `json:"replicas"`
	Claim               string      `json:"claim"`
	StagingClaim        string      `json:"stagingClaim"`
	ConfigurationSecret string      `json:"configurationSecret"`
	BootstrapSecret     string      `json:"bootstrapSecret,omitempty"`
	Backup              *BackupSpec `json:"backup,omitempty"`
	Ingress             IngressSpec `json:"ingress"`
	SSH                 SSHSpec     `json:"ssh,omitempty"`
}
type Operation struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Phase        string `json:"phase"`
	Version      string `json:"version"`
	Claim        string `json:"claim"`
	StagingClaim string `json:"stagingClaim"`
	Request      string `json:"request,omitempty"`
	Generation   int64  `json:"generation"`
	Message      string `json:"message,omitempty"`
	JobCreated   bool   `json:"jobCreated,omitempty"`
	JobUID       string `json:"jobUID,omitempty"`
}
type BackupResult struct {
	Request     string      `json:"request,omitempty"`
	Operation   string      `json:"operation,omitempty"`
	Snapshot    string      `json:"snapshot,omitempty"`
	Message     string      `json:"message,omitempty"`
	Success     bool        `json:"success"`
	CompletedAt metav1.Time `json:"completedAt"`
}
type ForgejoApplicationStatus struct {
	ObservedGeneration       int64              `json:"observedGeneration,omitempty"`
	Conditions               []metav1.Condition `json:"conditions,omitempty"`
	PreparedVersion          string             `json:"preparedVersion,omitempty"`
	PreparedClaim            string             `json:"preparedClaim,omitempty"`
	Operation                *Operation         `json:"operation,omitempty"`
	Backup                   *BackupResult      `json:"backup,omitempty"`
	LastHandledBackupRequest string             `json:"lastHandledBackupRequest,omitempty"`
}
type ForgejoApplication struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ForgejoApplicationSpec   `json:"spec,omitempty"`
	Status            ForgejoApplicationStatus `json:"status,omitempty"`
}
type ForgejoApplicationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ForgejoApplication `json:"items"`
}

func (a *ForgejoApplication) DeepCopy() *ForgejoApplication {
	if a == nil {
		return nil
	}
	b := *a
	b.ObjectMeta = *a.ObjectMeta.DeepCopy()
	if a.Spec.Backup != nil {
		x := *a.Spec.Backup
		b.Spec.Backup = &x
	}
	b.Status.Conditions = append([]metav1.Condition(nil), a.Status.Conditions...)
	if a.Status.Operation != nil {
		x := *a.Status.Operation
		b.Status.Operation = &x
	}
	if a.Status.Backup != nil {
		x := *a.Status.Backup
		b.Status.Backup = &x
	}
	return &b
}
func (a *ForgejoApplication) DeepCopyObject() runtime.Object { return a.DeepCopy() }
func (a *ForgejoApplicationList) DeepCopyObject() runtime.Object {
	if a == nil {
		return (*ForgejoApplicationList)(nil)
	}
	b := *a
	b.ListMeta = *a.ListMeta.DeepCopy()
	b.Items = make([]ForgejoApplication, len(a.Items))
	for i := range a.Items {
		b.Items[i] = *a.Items[i].DeepCopy()
	}
	return &b
}
