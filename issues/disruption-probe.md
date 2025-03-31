# Feature request / problem statement

Our users rely heavily on eviction and pod disruption budgets for stability and for fleet management, see [this previous talk on rotating nodes](https://www.youtube.com/watch?v=KQ1obaC-ht0) for more context.

An issue our users often run into is that temporarily they do not want their pod to be evicted. The readiness probe is not an option because the pods critically do still need to serve traffic.

There are several examples where application owners had to build workarounds for the current behavior to distinguish between these two states:

### Example 1)

We are running a custom-built distributed database. On pod startup, it is assigned a shard and synchronizes data with its siblings in the background. It can be configured to serve traffic once it has replicated enough data while continuing to sync in the background.
This means that the cluster is in a state where we need to serve traffic for stability reasons, but can't afford to lose another pod of the same shard during that time.

Readiness probes could be used by increasing the shard replica count, but it's tightly connected to the total cost of the application, which will increase significantly when running many small clusters.

### Example 2)

The Elasticsearch operator has a similar problem. Elasticsearch clusters can be in different [health states (green / yellow / red)](https://www.elastic.co/guide/en/elasticsearch/reference/current/cluster-health.html). If the cluster health is not green, it means that it could still be ready, but the system shouldn't disrupt any of the pods.

Unfortunately, they can't rely on readiness probes only. If a [cluster is in a yellow state](https://www.elastic.co/guide/en/elasticsearch/reference/current/red-yellow-cluster-status.html), it means, for example, that one of the shards is missing replicas. So there should not be any disruption to prevent data loss, but the cluster nodes should still be ready to be able to serve traffic.

To mitigate the problem, the operator maintains logic to [update the cluster's PDB](https://github.com/elastic/cloud-on-k8s/blob/v2.16.1/pkg/controller/elasticsearch/pdb/reconcile.go#L193-L197) and change the `minAvailable` count depending on the health.

### Impact

There are more cases like this, especially for stateful workloads. We have built multiple workarounds, such as implementing custom eviction API endpoints that behave similarly to the Kubernetes API but give more control to our users.

In all these cases, the failure domain is moved from pod level to an external controller. Ensuring application cluster stability is therefore coupled with the health of this controller, which is often less maintained and monitored. Furthermore, users have to actively work against Kubernetes primitives to reflect business needs.

The request is to have a mechanism provided by Kubernetes that can distinguish between whether a pod should be routable (readiness) and whether a pod should be disruptable. If not provided, the behavior will be as is today with readiness controlling disruptability. The solution outlined here is to have a disruption probe and pod status, similar to readiness.

# Proposal

The proposal is to add an additional probe the kubelet would perform, similar to [liveness and readiness](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/). See [probe definition](https://pkg.go.dev/k8s.io/api/core/v1#Probe), an example portion of the pod spec:

```
"readinessProbe": { # existing
    ...
},
"disruptionProbe": { # new
    "failureThreshold": 3,
    "httpGet": {
        "path": "/disruptable",
        "port": 8484,
        "scheme": "HTTP"
    },
    "initialDelaySeconds": 60,
    "periodSeconds": 30,
    "successThreshold": 1,
    "timeoutSeconds": 1
},

```

Similarly there will be a corresponding status on the pod, an example portion of the pod status:

```
"status": {
"conditions": [
...
    { # existing
        ...
        "status": "True",
        "type": "Ready"
    },
    { # new
        "lastTransitionTime": "2025-02-05T20:26:15Z",
        "status": "True",
        "type": "Disruptable"
    },
    ...
  ]
```

## Behavior, Implementation, and Details

If the disruption probe is defined, only pods with a `True` `Disruptable` condition status (see above status) will count towards the quota for disruption. The disruption controller will need to take this into account _instead_ of the `Ready` condition of pods it looks at today. To maintain current behaviour, if no disruption probe is defined, the kubelet will sync the `Ready` status to the `Disruptable` condition.

It's important that the system we build to support this use case fails closed. Meaning that if it doesn't work correctly we don't accidentally allow evictions that should not be allowed. Probes give this, because having a disruption probe initiated by the kubelet keeps the failure domain consistent with the kubelet, meaning that the eviction won't happen unless the kubelet is a healthy member of the cluster _and_ the kubelet gets an OK response from the relevant pods. An alternate solution could be to have a centralized gate controller effectively manage the `Disruptable` condition. This alternative disruption gates solution is not selected here since among other problems it would be difficult to make its behaviour fail closed.

## Next steps

We are hoping to gauge interest with a lighter weight issue, if there is interest we will convert to a KEP.
