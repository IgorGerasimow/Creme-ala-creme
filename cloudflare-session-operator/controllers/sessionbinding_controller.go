package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Creme-ala-creme/cloudflare-session-operator/api/v1alpha1"
	"github.com/Creme-ala-creme/cloudflare-session-operator/pkg/cloudflare"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	sessionBindingFinalizer = "sessionbinding.cloudflare.example.com/finalizer"
	podSessionLabelKey      = "cloudflare.example.com/session-id"
)

// SessionBindingReconciler reconciles a SessionBinding object
type SessionBindingReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	CFClient cloudflare.Client
	Recorder recordEventRecorder
	Clock    Clock
}

type recordEventRecorder interface {
	Event(object runtime.Object, eventtype, reason, message string)
}

// Clock abstracts time-related functionality for easier testing.
type Clock interface {
	Now() time.Time
}

// RealClock implements Clock using the standard library.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

//+kubebuilder:rbac:groups=cloudflare.example.com,resources=sessionbindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cloudflare.example.com,resources=sessionbindings/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=cloudflare.example.com,resources=sessionbindings/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *SessionBindingReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	binding := &v1alpha1.SessionBinding{}
	if err := r.Get(ctx, req.NamespacedName, binding); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !binding.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, logger, binding)
	}

	if !controllerutil.ContainsFinalizer(binding, sessionBindingFinalizer) {
		controllerutil.AddFinalizer(binding, sessionBindingFinalizer)
		if err := r.Update(ctx, binding); err != nil {
			return ctrl.Result{}, err
		}
	}

	binding.Status.ObservedGeneration = binding.Generation
	now := metav1.Time{Time: r.Clock.Now()}
	binding.Status.LastReconcileTime = &now

	result, reconcileErr := r.reconcileActive(ctx, logger, binding)
	statusErr := r.patchStatus(ctx, binding)
	if reconcileErr != nil {
		return result, reconcileErr
	}
	return result, statusErr
}

func (r *SessionBindingReconciler) reconcileActive(ctx context.Context, logger logr.Logger, binding *v1alpha1.SessionBinding) (ctrl.Result, error) {
	if binding.Spec.SessionID == "" {
		err := errors.New("spec.sessionID must be provided")
		logger.Error(err, "invalid SessionBinding spec")
		r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionSessionDiscovered, metav1.ConditionFalse, "InvalidSpec", err.Error())
		binding.Status.Phase = v1alpha1.SessionBindingPhaseError
		return ctrl.Result{}, nil
	}

	sessionExists, sessionErr := r.CFClient.EnsureSession(ctx, binding.Spec.SessionID)
	if sessionErr != nil {
		logger.Error(sessionErr, "failed to verify Cloudflare session")
		r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionSessionDiscovered, metav1.ConditionUnknown, "CloudflareError", sessionErr.Error())
		binding.Status.Phase = v1alpha1.SessionBindingPhaseError
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	if !sessionExists {
		logger.Info("Cloudflare session missing; marking binding expired", "sessionID", binding.Spec.SessionID)
		r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionSessionDiscovered, metav1.ConditionFalse, "NotFound", "Cloudflare session not found")
		binding.Status.Phase = v1alpha1.SessionBindingPhaseExpired
		return ctrl.Result{}, nil
	}

	r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionSessionDiscovered, metav1.ConditionTrue, "SessionActive", "Cloudflare session is active")

	pod, err := r.ensureSessionPod(ctx, logger, binding)
	if err != nil {
		binding.Status.Phase = v1alpha1.SessionBindingPhaseError
		return ctrl.Result{}, err
	}

	if !isPodReady(pod) {
		r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionPodReady, metav1.ConditionFalse, "WaitingForReadiness", "Session pod not ready yet")
		binding.Status.Phase = v1alpha1.SessionBindingPhasePending
		binding.Status.BoundPod = pod.Name
		binding.Status.RouteEndpoint = ""
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionPodReady, metav1.ConditionTrue, "PodReady", "Session pod ready")

	endpoint := podEndpoint(pod)
	if endpoint == "" {
		r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionRouteConfigured, metav1.ConditionFalse, "PodEndpointMissing", "Pod ready but lacks PodIP/port")
		binding.Status.Phase = v1alpha1.SessionBindingPhaseError
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if err := r.CFClient.EnsureRoute(ctx, binding.Spec.SessionID, endpoint); err != nil {
		logger.Error(err, "failed to configure Cloudflare route", "sessionID", binding.Spec.SessionID, "endpoint", endpoint)
		r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionRouteConfigured, metav1.ConditionFalse, "CloudflareError", err.Error())
		binding.Status.Phase = v1alpha1.SessionBindingPhaseError
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	binding.Status.Phase = v1alpha1.SessionBindingPhaseBound
	binding.Status.BoundPod = pod.Name
	binding.Status.RouteEndpoint = endpoint
	r.setCondition(&binding.Status.Conditions, v1alpha1.ConditionRouteConfigured, metav1.ConditionTrue, "RouteConfigured", "Cloudflare route configured")
	return ctrl.Result{}, nil
}

func (r *SessionBindingReconciler) ensureSessionPod(ctx context.Context, logger logr.Logger, binding *v1alpha1.SessionBinding) (*corev1.Pod, error) {
	podName := fmt.Sprintf("session-%s", binding.Spec.SessionID)
	pod := &corev1.Pod{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: binding.Namespace, Name: podName}, pod); err == nil {
		return pod, nil
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}

	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: binding.Namespace, Name: binding.Spec.TargetDeployment}, deployment); err != nil {
		logger.Error(err, "target deployment not found", "deployment", binding.Spec.TargetDeployment)
		return nil, err
	}

	template := deployment.Spec.Template.DeepCopy()
	if template.Labels == nil {
		template.Labels = map[string]string{}
	}
	template.Labels[podSessionLabelKey] = binding.Spec.SessionID
	template.Labels["app.kubernetes.io/managed-by"] = "cloudflare-session-operator"

	pod = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   binding.Namespace,
			Labels:      template.Labels,
			Annotations: template.Annotations,
		},
		Spec: template.Spec,
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[podSessionLabelKey] = binding.Spec.SessionID

	if err := controllerutil.SetControllerReference(binding, pod, r.Scheme); err != nil {
		return nil, err
	}

	if err := r.Create(ctx, pod); err != nil {
		return nil, err
	}

	r.Recorder.Event(binding, corev1.EventTypeNormal, "PodCreated", fmt.Sprintf("Created pod %s for session %s", pod.Name, binding.Spec.SessionID))
	return pod, nil
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func podEndpoint(pod *corev1.Pod) string {
	if pod.Status.PodIP == "" {
		return ""
	}
	port := int32(80)
	for _, container := range pod.Spec.Containers {
		if len(container.Ports) > 0 {
			port = container.Ports[0].ContainerPort
			break
		}
	}
	return fmt.Sprintf("%s:%d", pod.Status.PodIP, port)
}

func (r *SessionBindingReconciler) handleDeletion(ctx context.Context, logger logr.Logger, binding *v1alpha1.SessionBinding) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(binding, sessionBindingFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := r.cleanupResources(ctx, logger, binding); err != nil {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(binding, sessionBindingFinalizer)
	if err := r.Update(ctx, binding); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SessionBindingReconciler) cleanupResources(ctx context.Context, logger logr.Logger, binding *v1alpha1.SessionBinding) error {
	if binding.Status.BoundPod != "" {
		pod := &corev1.Pod{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: binding.Namespace, Name: binding.Status.BoundPod}, pod); err == nil {
			if err := r.Delete(ctx, pod); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
		}
	}

	if binding.Spec.SessionID != "" {
		if err := r.CFClient.DeleteRoute(ctx, binding.Spec.SessionID); err != nil {
			logger.Error(err, "failed to delete Cloudflare route during cleanup", "sessionID", binding.Spec.SessionID)
			return err
		}
	}

	r.Recorder.Event(binding, corev1.EventTypeNormal, "CleanedUp", "Removed Cloudflare route and session pod")
	return nil
}

func (r *SessionBindingReconciler) patchStatus(ctx context.Context, binding *v1alpha1.SessionBinding) error {
	current := &v1alpha1.SessionBinding{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: binding.Namespace, Name: binding.Name}, current); err != nil {
		return err
	}

	if equality.Semantic.DeepEqual(current.Status, binding.Status) {
		return nil
	}

	current.Status = binding.Status
	return r.Status().Update(ctx, current)
}

func (r *SessionBindingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SessionBinding{}).
		Owns(&corev1.Pod{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}

func (r *SessionBindingReconciler) setCondition(conditions *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:    condType,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}
