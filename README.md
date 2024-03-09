# Port-Bridge

Port-Bridge simplifies Kubernetes service exposure by providing a unified solution to manage node ports and external traffic through a single load balancer. This operator automates the process of creating, updating, and managing network traffic distribution to Kubernetes services, reducing the complexity and cost associated with maintaining multiple load balancers.

## Features

- **Unified Load Balancer Management:** Centralize the management of external traffic through a single load balancer for multiple services.
- **Dynamic Service Discovery:** Automatically detects and configures new services that require external exposure.
- **Cost Efficiency:** Reduce the costs associated with provisioning and maintaining multiple load balancers.
- **Customizable Traffic Distribution:** Easily configure rules for traffic distribution among services.
- **High Availability:** Ensures high availability of services with intelligent health checks and failover mechanisms.

### Running on the cluster
1. Install Instances of Custom Resources Definitions (CRDs) into the cluster:

```sh
kubectl apply -f config/crd/bases/
```
or

```sh
kubectl apply -f https://raw.githubusercontent.com/abdheshnayak/port-bridge/main/config/crd/bases/crds.anayak.com.np_portbridgeservices.yaml
```

2. Create a PortBridgeService Custom Resource (CR) to expose a service:
    
```yaml
apiVersion: crds.anayak.com.np/v1
kind: PortBridgeService
metadata:
  name: portbridgeservice-sample
spec:
  namespaces:
    - default
  replicas: 1
```

> **NOTE:** The `namespaces` field specifies the namespaces where the services are located. The `replicas` field specifies the number of replicas for the load balancer.

3. Add the following label to the service you want to expose:

```yaml
metadata:
  labels:
    anayak.com.np/port-bridge-service: "true"
```

> **NOTE:** The `anayak.com.np/port-bridge-service` label is used to identify the services that need to be exposed through the Port-Bridge.

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

