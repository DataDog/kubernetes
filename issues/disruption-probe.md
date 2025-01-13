# Feature request / problem statement

Our users rely heavily on eviction and pod disruption budgets for stability and for fleet management, see TODO link talks for more context.

An issue our users often run into is that the do not want their pod to be evicted temporarily. The readiness probe is not an option because the pods critically do still need to serve traffic. TODO small justification / real example.
 
The request is to have a mechanism provided by Kubernetes that can distinguish between whether a pod should be routable (readiness) and whether a pod should be disruptable. The solution outlined here is to have a disruption probe and pod status, similar to readiness.

# Proposal

The proposal is to add an additional probe the kubelet would perform, similar to [liveness and readiness](https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readine), for example:

```
TODO example, it's very simple
```

Similarly there will be a corresponding status on the pod. 

```
TODO example, it's very simple
```

## Behavior, Implementation, and Details

Only pods with a healthy disruption status (see above status) will be eiligble for disruption. This means the disruption controller will need to take this into account *instead* of the readiness of pods it looks at today.

There is a concern around starvation, meaning that pods could in theory (and likely in practice) never allow themselves to be disrupted. This concern exists for readiness too, but there is a natural pushback on the user as during this time the won't be routed to so it cannot continue business as usual. It may be worth considering limitations on how long a pod can be considered "not disruptable".

TODO more info on how it works with readiness and how importance of failing closed

## Next steps

We are hoping to gauge interest with a lighter weight issue, if there is interest we will convert to a KEP.

