package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	crdsv1 "github.com/abdheshnayak/port-bridge/api/v1"
	"github.com/abdheshnayak/port-bridge/controllers/env"
	"github.com/kloudlite/operator/pkg/constants"
	fn "github.com/kloudlite/operator/pkg/functions"
	"github.com/kloudlite/operator/pkg/kubectl"
	"github.com/kloudlite/operator/pkg/logging"
	rApi "github.com/kloudlite/operator/pkg/operator"
	stepResult "github.com/kloudlite/operator/pkg/operator/step-result"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// apiFields "k8s.io/apimachinery/pkg/fields"
	apiLabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	SvcNameKey = "anayak.com.np/port-bridge-service.name"
	SvcMarkKey = "anayak.com.np/port-bridge-service"
)

// Reconciler reconciles a PortBridgeService object
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme

	logger     logging.Logger
	Name       string
	yamlClient kubectl.YAMLClient
	Env        *env.Env
}

//+kubebuilder:rbac:groups=crds.anayak.com.np,resources=portbridgeservices,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=crds.anayak.com.np,resources=portbridgeservices/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=crds.anayak.com.np,resources=portbridgeservices/finalizers,verbs=update

const (
	PBSvcDeleted              = "port-bridge-service-deleted"
	NodeportConfigAndSvcReady = "nodeport-config-and-svc-ready"
)

func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	req, err := rApi.NewRequest(rApi.NewReconcilerCtx(ctx, r.logger), r.Client, request.NamespacedName, &crdsv1.PortBridgeService{})
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if req.Object.GetDeletionTimestamp() != nil {
		if x := r.finalize(req); !x.ShouldProceed() {
			return x.ReconcilerResponse()
		}

		return ctrl.Result{}, nil
	}

	req.PreReconcile()
	defer req.PostReconcile()

	if step := req.ClearStatusIfAnnotated(); !step.ShouldProceed() {
		return step.ReconcilerResponse()
	}

	if step := req.EnsureChecks(); !step.ShouldProceed() {
		return step.ReconcilerResponse()
	}

	if step := req.EnsureLabelsAndAnnotations(); !step.ShouldProceed() {
		return step.ReconcilerResponse()
	}

	if step := req.EnsureFinalizers(constants.ForegroundFinalizer, constants.CommonFinalizer); !step.ShouldProceed() {
		return step.ReconcilerResponse()
	}

	if step := r.reconNodeportConfigAndSvc(req); !step.ShouldProceed() {
		return step.ReconcilerResponse()
	}

	req.Object.Status.IsReady = true
	return ctrl.Result{}, nil
}

func (r *Reconciler) reconNodeportConfigAndSvc(req *rApi.Request[*crdsv1.PortBridgeService]) stepResult.Result {
	ctx, obj := req.Context(), req.Object

	check := rApi.Check{Generation: obj.Generation}

	failed := func(err error) stepResult.Result {
		return req.CheckFailed(NodeportConfigAndSvcReady, check, err.Error())
	}

	var services corev1.ServiceList
	if err := r.List(ctx, &services, &client.ListOptions{
		LabelSelector: apiLabels.SelectorFromValidatedSet(map[string]string{
			SvcMarkKey: "true",
		}),
	}); err != nil {
		r.logger.Error(err)
	}

	nodeports := map[int32]metaData{}

	for _, svc := range services.Items {
		if !slices.Contains(obj.Spec.Namespaces, svc.GetNamespace()) || svc.Spec.Type != corev1.ServiceTypeNodePort {
			continue
		}

		for _, port := range svc.Spec.Ports {
			ip := ""

			if len(svc.Spec.ClusterIPs) > 0 {
				ip = svc.Spec.ClusterIPs[0]
			}

			if port.NodePort != 0 {
				nodeports[port.NodePort] = metaData{
					Name:      svc.GetName(),
					Protocol:  port.Protocol,
					Namespace: svc.GetNamespace(),
					Ip:        ip,
					Port:      port.Port,
				}
			}
		}
	}

	if len(nodeports) == 0 {
		return failed(fmt.Errorf("no nodeports found"))
	}

	needsToUpdate := func() bool {
		cm, err := rApi.Get(ctx, r.Client, fn.NN("default", fmt.Sprintf("%s-config", obj.GetName())), &corev1.ConfigMap{})
		if err != nil {
			r.logger.Error(err)
			return true
		}

		oldNodeportsString := cm.Data["nodeports"]

		oldNodeports := map[int32]metaData{}

		if err := json.Unmarshal([]byte(oldNodeportsString), &oldNodeports); err != nil {
			return true
		}

		if equal := maps.Equal(nodeports, oldNodeports); !equal {
			fmt.Println("Nodeports are not equal")
			return true
		}

		// if the service or deployment is not found, we need to update
		if _, err := rApi.Get(ctx, r.Client, fn.NN("default", fmt.Sprintf("%s-svc", obj.GetName())), &corev1.Service{}); err != nil {
			if apiErrors.IsNotFound(err) {
				return true
			}
		}

		if _, err := rApi.Get(ctx, r.Client, fn.NN("default", fmt.Sprintf("%s-deployment", obj.GetName())), &appsv1.Deployment{}); err != nil {
			if apiErrors.IsNotFound(err) {
				return true
			}
		}

		return false
	}()

	if needsToUpdate {
		jsonStr, err := json.Marshal(nodeports)
		if err != nil {
			return failed(err)
		}

		cm := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "ConfigMap",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-config", obj.GetName()),
				Namespace: "default",
				Labels: map[string]string{
					SvcMarkKey: "true",
					SvcNameKey: obj.GetName(),
				},
				Annotations: map[string]string{
					SvcMarkKey: "true",
					SvcNameKey: obj.GetName(),
				},
				OwnerReferences: []metav1.OwnerReference{fn.AsOwner(obj, true)},
			},
			Data: map[string]string{
				"nodeports": string(jsonStr),
			},
		}

		if err := fn.KubectlApply(ctx, r.Client, cm); err != nil {
			return failed(err)
		}
	}

	if needsToUpdate {
		svc := &corev1.Service{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Service",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-svc", obj.GetName()),
				Namespace: "default",
				Labels: map[string]string{
					SvcMarkKey: "true",
					SvcNameKey: obj.GetName(),
				},
				Annotations: map[string]string{
					SvcMarkKey: "true",
					SvcNameKey: obj.GetName(),
				},
				OwnerReferences: []metav1.OwnerReference{fn.AsOwner(obj, true)},
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{
					SvcMarkKey: "true",
					SvcNameKey: obj.GetName(),
				},
				Type: corev1.ServiceTypeLoadBalancer,
				Ports: func() []corev1.ServicePort {
					ports := []corev1.ServicePort{}
					for nodeport, svcName := range nodeports {
						ports = append(ports, corev1.ServicePort{
							Name:     fmt.Sprintf("%s-%d", svcName.Name, nodeport),
							Protocol: svcName.Protocol,
							Port:     nodeport,
						})
					}
					return ports
				}(),
			},
		}

		if err := fn.KubectlApply(ctx, r.Client, svc); err != nil {
			return failed(err)
		}
	}

	if needsToUpdate {
		deployment := getDeployment(req, nodeports)
		if err := fn.KubectlApply(ctx, r.Client, deployment); err != nil {
			return failed(err)
		}
	}

	check.Status = true
	if check != req.Object.Status.Checks[NodeportConfigAndSvcReady] {
		fn.MapSet(&req.Object.Status.Checks, NodeportConfigAndSvcReady, check)
		if sr := req.UpdateStatus(); !sr.ShouldProceed() {
			return sr
		}
	}

	return req.Next()
}

func (r *Reconciler) finalize(req *rApi.Request[*crdsv1.PortBridgeService]) stepResult.Result {
	return req.Finalize()
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager, logger logging.Logger) error {
	r.Client = mgr.GetClient()
	r.Scheme = mgr.GetScheme()
	r.logger = logger.WithName(r.Name)
	r.yamlClient = kubectl.NewYAMLClientOrDie(mgr.GetConfig(), kubectl.YAMLClientOpts{Logger: r.logger})

	builder := ctrl.NewControllerManagedBy(mgr)

	builder.For(&crdsv1.PortBridgeService{})

	watchlist := []client.Object{
		&corev1.Service{},
		&corev1.ConfigMap{},
		&appsv1.Deployment{},
	}

	for _, obj := range watchlist {
		builder.Watches(obj, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {

			result := []reconcile.Request{}
			if o.GetAnnotations()[SvcMarkKey] != "true" {
				return result
			}

			pbsList := &crdsv1.PortBridgeServiceList{}
			if err := r.List(ctx, pbsList); err != nil {
				return nil
			}

			for _, pbs := range pbsList.Items {
				if slices.Contains(pbs.Spec.Namespaces, o.GetNamespace()) || o.GetNamespace() == "default" {
					result = append(result, reconcile.Request{
						NamespacedName: client.ObjectKey{
							Name: pbs.GetName(),
						},
					})
				}
			}

			return result
		}))
	}

	return builder.Complete(r)
}
