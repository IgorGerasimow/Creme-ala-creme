package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SessionBindingPhase represents the lifecycle phase of a session binding.
type SessionBindingPhase string

const (
	SessionBindingPhasePending SessionBindingPhase = "Pending"
	SessionBindingPhaseBound   SessionBindingPhase = "Bound"
	SessionBindingPhaseExpired SessionBindingPhase = "Expired"
	SessionBindingPhaseError   SessionBindingPhase = "Error"
)

// SessionBindingSpec defines the desired state of SessionBinding.
type SessionBindingSpec struct {
	// SessionID is the Cloudflare session identifier to bind.
	SessionID string `json:"sessionID"`
	// UserID is an optional identifier for the user owning the session.
	// +optional
	UserID string `json:"userID,omitempty"`
	// TargetDeployment references the deployment that should be cloned for session pods.
	TargetDeployment string `json:"targetDeployment"`
	// TTLSeconds defines how long the binding should remain active after creation.
	// +optional
	TTLSeconds *int64 `json:"ttlSeconds,omitempty"`
}

// SessionBindingStatus defines the observed state of SessionBinding.
type SessionBindingStatus struct {
	Phase SessionBindingPhase `json:"phase,omitempty"`
	// BoundPod is the name of the pod created for this session.
	BoundPod string `json:"boundPod,omitempty"`
	// RouteEndpoint is the endpoint programmed in Cloudflare for this session.
	RouteEndpoint string `json:"routeEndpoint,omitempty"`
	// ObservedGeneration tracks the latest processed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions represent the latest available observations of the binding state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// LastReconcileTime records the last time the controller reconciled the resource.
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// SessionBinding is the Schema for the sessionbindings API.
type SessionBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SessionBindingSpec   `json:"spec,omitempty"`
	Status SessionBindingStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// SessionBindingList contains a list of SessionBinding.
type SessionBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SessionBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SessionBinding{}, &SessionBindingList{})
}

const (
	// Condition types for status management.
	ConditionSessionDiscovered = "SessionDiscovered"
	ConditionPodReady          = "PodReady"
	ConditionRouteConfigured   = "RouteConfigured"
)
