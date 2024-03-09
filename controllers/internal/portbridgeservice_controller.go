package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/kloudlite/operator/pkg/constants"
	fn "github.com/kloudlite/operator/pkg/functions"
	"github.com/kloudlite/operator/pkg/kubectl"
	"github.com/kloudlite/operator/pkg/logging"
	rApi "github.com/kloudlite/operator/pkg/operator"

	appsv1 "k8s.io/api/apps/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiFields "k8s.io/apimachinery/pkg/fields"
	apiLabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdsv1 "github.com/abdheshnayak/port-bridge/api/v1"
	"github.com/abdheshnayak/port-bridge/controllers/internal/env"
	stepResult "github.com/kloudlite/operator/pkg/operator/step-result"
	corev1 "k8s.io/api/core/v1"
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

func (r *Reconciler) createOrRollOutDeployment(req *rApi.Request[*crdsv1.PortBridgeService]) error {
	ctx, obj := req.Context(), req.Object
	d, err := rApi.Get(ctx, r.Client, fn.NN(fmt.Sprintf("%s-deployment", obj.GetName()), "default"), &appsv1.Deployment{})
	if err != nil {
		if !apiErrors.IsNotFound(err) {
			return err
		}
	}

	if d != nil {
		if err := fn.RolloutRestart(r.Client, fn.Deployment, "default", map[string]string{
			SvcMarkKey: "true",
			SvcNameKey: obj.GetName(),
		}); err != nil {
			return err
		}
	}

	deployment := getDeployment(req)
	if err := fn.KubectlApply(ctx, r.Client, deployment); err != nil {
		return err
	}

	return nil
}

func (r *Reconciler) reconNodeportConfigAndSvc(req *rApi.Request[*crdsv1.PortBridgeService]) stepResult.Result {
	ctx, obj := req.Context(), req.Object

	check := rApi.Check{Generation: obj.Generation}

	failed := func(err error) stepResult.Result {
		return req.CheckFailed(NodeportConfigAndSvcReady, check, err.Error())
	}

	var services corev1.ServiceList
	if err := r.List(ctx, &services, &client.ListOptions{
		FieldSelector: client.MatchingFieldsSelector{
			Selector: apiFields.OneTermEqualSelector("spec.type", "NodePort"),
		},
		LabelSelector: apiLabels.SelectorFromValidatedSet(map[string]string{
			SvcMarkKey: "true",
		}),
	}); err != nil {
		if !apiErrors.IsNotFound(err) {
			return failed(err)
		}
	}

	type metaData struct {
		Name      string          `json:"name"`
		Protocol  corev1.Protocol `json:"protocol"`
		Namespace string          `json:"namespace"`
	}

	nodeports := map[string]metaData{}

	for _, svc := range services.Items {
		if !slices.Contains(obj.Spec.Namespaces, svc.GetNamespace()) {
			continue
		}

		for _, port := range svc.Spec.Ports {
			if port.NodePort != 0 {
				nodeports[fmt.Sprint(port.NodePort)] = metaData{
					Name:      svc.GetName(),
					Protocol:  port.Protocol,
					Namespace: svc.GetNamespace(),
				}
			}
		}
	}

	if len(nodeports) == 0 {
		return failed(fmt.Errorf("no nodeports found"))
	}

	needsToUpdate := func() bool {
		cm, err := rApi.Get(ctx, r.Client, fn.NN("port-bridge-config", "default"), &corev1.ConfigMap{})
		if err != nil {
			return true
		}

		oldNodeportsString := cm.Data["nodeports"]

		oldNodeports := map[string]metaData{}

		if err := json.Unmarshal([]byte(oldNodeportsString), &oldNodeports); err != nil {
			return true
		}

		if equal := maps.Equal(nodeports, oldNodeports); !equal {
			return true
		}

		return false
	}()

	if needsToUpdate {
		jsonStr, err := json.Marshal(nodeports)
		if err != nil {
			return failed(err)
		}

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-config", obj.GetName()),
				Namespace: "default",
				Labels: map[string]string{
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
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-svc", obj.GetName()),
				Namespace: "default",
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
				// Type: corev1.ServiceTypeLoadBalancer,
				Type: corev1.ClusterIPNone,
				Ports: func() []corev1.ServicePort {
					ports := []corev1.ServicePort{}
					for nodeport, svcName := range nodeports {
						np, _ := strconv.Atoi(nodeport)
						ports = append(ports, corev1.ServicePort{
							Name:     fmt.Sprintf("%s:%s", svcName.Name, nodeport),
							Protocol: svcName.Protocol,
							Port:     int32(np),
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
		if err := r.createOrRollOutDeployment(req); err != nil {
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
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr)

	builder.For(&crdsv1.PortBridgeService{})

	builder.Watches(&corev1.Service{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {

		result := []reconcile.Request{}

		if o.GetAnnotations()[SvcMarkKey] != "true" {
			return result
		}

		pbsList := &crdsv1.PortBridgeServiceList{}
		if err := r.List(ctx, pbsList); err != nil {
			return nil
		}

		for _, pbs := range pbsList.Items {

			if slices.Contains(pbs.Spec.Namespaces, o.GetNamespace()) {
				result = append(result, reconcile.Request{
					NamespacedName: client.ObjectKey{
						Name: pbs.GetName(),
					},
				})
			}
		}

		return result
	}))

	return builder.Complete(r)
}
