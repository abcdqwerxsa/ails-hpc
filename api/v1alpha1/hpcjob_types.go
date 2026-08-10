package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/runtime/schema"
	metav1types "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// HpcJobSpec defines the desired state of HpcJob
type HpcJobSpec struct {
	JobType           string   `json:"jobType,omitempty"`
	Image             string   `json:"image"`
	Slots             int32    `json:"slots"`
	Command           []string `json:"command,omitempty"`
	Queue             string   `json:"queue,omitempty"`
	TenantNamespace   string   `json:"tenantNamespace,omitempty"`
	StorageSize       string   `json:"storageSize,omitempty"`       // Local-Path PVC Size e.g. "5Gi"
	PriorityClassName string   `json:"priorityClassName,omitempty"` // Kueue Priority Class
}

// HpcJobStatus defines the observed state of HpcJob
type HpcJobStatus struct {
	Phase             string                  `json:"phase,omitempty"`
	Conditions        []metav1types.Condition `json:"conditions,omitempty"`
	StartTime         *metav1types.Time       `json:"startTime,omitempty"`
	CompletionTime    *metav1types.Time       `json:"completionTime,omitempty"`
	CoreHours         float64                 `json:"coreHours,omitempty"`         // Calculated CPU Core-Hours
	ExecutionDuration string                  `json:"executionDuration,omitempty"` // Formatted Duration e.g. "45s"
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// HpcJob is the Schema for the hpcjobs API
type HpcJob struct {
	metav1types.TypeMeta   `json:",inline"`
	metav1types.ObjectMeta `json:"metadata,omitempty"`

	Spec   HpcJobSpec   `json:"spec,omitempty"`
	Status HpcJobStatus `json:"status,omitempty"`
}

func (in *HpcJob) DeepCopyInto(out *HpcJob) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	if in.Spec.Command != nil {
		in, out := &in.Spec.Command, &out.Spec.Command
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	out.Status = in.Status
	if in.Status.StartTime != nil {
		in, out := &in.Status.StartTime, &out.Status.StartTime
		*out = (*in).DeepCopy()
	}
	if in.Status.CompletionTime != nil {
		in, out := &in.Status.CompletionTime, &out.Status.CompletionTime
		*out = (*in).DeepCopy()
	}
}

func (in *HpcJob) DeepCopy() *HpcJob {
	if in == nil {
		return nil
	}
	out := new(HpcJob)
	in.DeepCopyInto(out)
	return out
}

func (in *HpcJob) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// +kubebuilder:object:root=true
// HpcJobList contains a list of HpcJob
type HpcJobList struct {
	metav1types.TypeMeta `json:",inline"`
	metav1types.ListMeta `json:"metadata,omitempty"`
	Items                []HpcJob `json:"items"`
}

func (in *HpcJobList) DeepCopyInto(out *HpcJobList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]HpcJob, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

func (in *HpcJobList) DeepCopy() *HpcJobList {
	if in == nil {
		return nil
	}
	out := new(HpcJobList)
	in.DeepCopyInto(out)
	return out
}

func (in *HpcJobList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

var (
	GroupVersion = metav1.GroupVersion{Group: "ails.hpc", Version: "v1alpha1"}
)

func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &HpcJob{}, &HpcJobList{})
	metav1types.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
