# CoreOS reboot operator

A Kubernetes [operator](https://coreos.com/blog/introducing-operators.html) that manages the reboot cycle for CoreOS nodes. Normally when a node self-updates, it waits to be rebooted in order for the changes to be effected. This has been traditionally done either by manual intervention or by sync tools like [locksmith](https://github.com/coreos/locksmith). Although the latter works very well, it does not offer full programmatic extensibility that's needed by some orgs who require high availability for their Kubernetes clusters.

This project was inspired by Aaron Levy's [KubeCon talk](https://www.youtube.com/watch?v=_BuqPMlXfpE) and is heavily based on his [demo controller](https://github.com/aaronlevy/kube-controller-demo) repository. Although this project has been verified to work, it's still very much in alpha so it's advised to use this in dev environments only.

## How it works

The operator is composed of two components: the `controller` which synchronizes the reboots, ensuring that the cluster will not be negatively impacted; and the `agent` daemon set, which listens out for reboot requests on systemd and performs the reboot itself.

This is the lifecycle of a reboot:

1. The update engine detects a new update is available, then it downloads and installs. When the self-installation has completed, the engine notifies its  completion by updating its status to `UPDATE_STATUS_UPDATED_NEED_REBOOT`.
2. The operator listens on a DBus interface for this state change. When it detects that a reboot is needed, it tags the Kubernetes node with a  `reboot-needed` annotation.
3. The controller uses an [informer](http://jayunit100.blogspot.de/2015/10/kubernetes-informers-controllers.html) to fire hooks when node resources are updated. When the controller sees that a node is marked for reboot (i.e. it has a specific annotation), it will perform a series of checks to make sure the operation is permitted - for example it will enforce a node quota, ensuring that only a specific number are rebooted at once. If these conditions pass, it permits the operation to go ahead and marks the node as `reboot`.
4. The agent also uses an informer to listen out for node state changes. Once this controller gives the green light, the agent cordons the Kubernetes node, preventing further pods being scheduled. It then gracefully deletes pods from the node. Once this is done, it sends a reboot command over DBus and the node is rebooted.
5. After the reboot, the agent re-marks the node as schedulable and removes any reboot annotations.

## Further work

- Allow better configuration through TPRs or ConfigMaps
- Add some kind of E2E testing
- Upgrade to client-go v3 when released
- Support pod eviction if available
- Improve pod filtering so that specific types are not force deleted

## Prerequisites

- The nodes must disable auto-reboots. You can do so by following the [update strategy](https://coreos.com/os/docs/latest/update-strategies.html) docs, or by simply disabling locksmith:

```bash
systemctl stop locksmithd
```

## How to deploy

```bash
# Create reboot-operator ns
kubectl create -f manifests/namespace.yaml

# Create cluster roles and sa bindings
kubectl create -f manifests/cluster-role.yaml

# Create controller RS
kubectl create -f manifests/reboot-controller.yaml

# Create agent DS
kubectl create -f manifests/cluster-role.yaml
```

## Building

Build agent and controller binaries:

`make clean all`

Build agent and controller Docker images:

`make clean images`
