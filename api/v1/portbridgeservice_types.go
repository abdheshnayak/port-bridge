package v1

import (
	"github.com/kloudlite/operator/pkg/constants"
	rApi "github.com/kloudlite/operator/pkg/operator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PortBridgeServiceSpec struct {
	Namespaces []string `json:"namespaces"`
	Replicas   *int32   `json:"replicas,omitempty"`
}

type PortBridgeServiceStatus struct {
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster

// PortBridgeService is the Schema for the portbridgeservices API
type PortBridgeService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PortBridgeServiceSpec `json:"spec,omitempty"`
	Status rApi.Status           `json:"status,omitempty"`
}

func (d *PortBridgeService) EnsureGVK() {
	if d != nil {
		d.SetGroupVersionKind(GroupVersion.WithKind("PortBridgeService"))
	}
}

func (d *PortBridgeService) GetStatus() *rApi.Status {
	return &d.Status
}

func (d *PortBridgeService) GetEnsuredLabels() map[string]string {
	return map[string]string{}
}

func (d *PortBridgeService) GetEnsuredAnnotations() map[string]string {
	return map[string]string{
		constants.GVKKey: GroupVersion.WithKind("PortBridgeService").String(),
	}
}

//+kubebuilder:object:root=true

// PortBridgeServiceList contains a list of PortBridgeService
type PortBridgeServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PortBridgeService `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PortBridgeService{}, &PortBridgeServiceList{})
}
