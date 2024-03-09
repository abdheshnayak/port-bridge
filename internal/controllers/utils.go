package controllers

import (
	"fmt"

	crdsv1 "github.com/abdheshnayak/port-bridge/api/v1"
	"github.com/kloudlite/operator/pkg/functions"
	rApi "github.com/kloudlite/operator/pkg/operator"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Ptr[T any](v T) *T {
	return &v
}

func getDeployment(req *rApi.Request[*crdsv1.PortBridgeService]) *appsv1.Deployment {
	obj, name := req.Object, req.Object.Name

	labels := map[string]string{SvcMarkKey: "true", SvcNameKey: name}

	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            fmt.Sprintf("%s-deployment", name),
			Namespace:       "default",
			Labels:          labels,
			OwnerReferences: []metav1.OwnerReference{functions.AsOwner(obj, true)},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: Ptr(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "port-bridge",
						Image: "nginx",
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "config",
							MountPath: "/portmap/config",
						}},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: fmt.Sprintf("%s-config", name)}},
							},
						},
					},
				},
			},
		},
	}
}
