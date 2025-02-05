# Feature request / problem statement

Our users rely heavily on eviction and pod disruption budgets for stability and for fleet management, see [this previous talk on rotating nodes](https://www.youtube.com/watch?v=KQ1obaC-ht0) for more context. TODO any other talks worth showing?

An issue our users often run into is that temporarily they do not want their pod to be evicted. The readiness probe is not an option because the pods critically do still need to serve traffic. TODO small justification / real example internal and external (elastic search).

We have worked around this writing a controller that modifies the `spec.maxUnavailable` field, setting it to `0` to block disruption and setting it back to enable.
 
The request is to have a mechanism provided by Kubernetes that can distinguish between whether a pod should be routable (readiness) and whether a pod should be disruptable. The solution outlined here is to have a disruption probe and pod status, similar to readiness.

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
        "lastProbeTime": null,
        "lastTransitionTime": "2025-02-05T20:26:15Z",
        "status": "True",
        "type": "Disruptable"
    },
    ...
  ]
```

## Behavior, Implementation, and Details

If the disruption probe is defined, only pods with a `True` `Disruptable` condition status (see above status) will count towards the quota for disruption. The disruption controller will need to take this into account *instead* of the `Ready` condition of pods it looks at today.

There is a concern around starvation, meaning that pods could in theory (and likely in practice) never allow themselves to be disrupted. This concern exists for readiness too, but there is a natural pushback on the user as during this time the won't be routed to so it cannot continue business as usual. It may be worth considering limitations on how long a pod can be considered "not disruptable". It's worth noting that this users currently have an easy mechanism to block eviction, namely to create a PDB that does not allow any disruptions.

It's important that the system we build to support this use case fails closed. Meaning that if it doesn't work correctly we don't accidentally allow evictions that should not be allowed. Probes give this, because having a disruption probe initiated by the kubelet keeps the failure domain consistent with the kubelet, meaning that the eviction won't happen unless the kubelet is a healthy member of the cluster *and* the kubelet gets an OK response from the relevant pods. An alternate solution could be to have a centralized controller effectively manage the `Disruptable` condition, among other problems this system would likely be difficult to make fail closed.

## Next steps

We are hoping to gauge interest with a lighter weight issue, if there is interest we will convert to a KEP.

