## k8s-haproxy-peer-sync

A process to be run as a sidecar container inside an HAProxy multi-replicas Deployment/DaemonSet/StatefulSet (in Kubernetes) to keep the HAProxy `peers` section in sync at runtime.

Useful for keeping in-memory `stick-table`s in sync (through peers) across multiple HAProxy pod replicas.

Robust to scale-in/outs.

### try it out

0) Create a cluster where to deploy it.
   As an example we'll use a [k8s kind](https://kind.sigs.k8s.io/) cluster here:
   ```
   kind create cluster --name "kind" --config cluster.yaml
   ```
0) Apply the Example sticky backend
   ```
   kubectl --context kind-kind apply -f examples/sticky-backend.yaml
   ```
0) Apply the "Load balancer" as a single entrypoint to the kind cluster
   ```
   kubectl --context kind-kind apply -f examples/lb.yaml
   ```
0) Apply the multi-replicas HAProxy setup with the k8s-haproxy-peer-sync sidecar
   ```
   kubectl --context kind-kind apply -f examples/haproxy.yaml
   ```
0) Port forward the load balancer entrypoint to your local machine
   ```
   kubectl --context kind-kind port-forward svc/lb 8080:8080
   ```

Profit!

### debug

Run some e2e tests against the kind cluster setup:
```
bash ./tests.sh http://127.0.0.1:8080
```

You can inspect the 'syncer' logs via:
```
kubectl --context kind-kind logs -f haproxy-<REPLICA-NAME> -c haproxy-peer-sync
```

You can inspect the haproxy / haproxy Data Plane API logs via:
```
kubectl --context kind-kind logs -f haproxy-<REPLICA-NAME> -c haproxy
```

You can live debug the haproxy peers via:
```
kubectl --context kind-kind exec -it -c haproxy haproxy-<REPLICA-NAME> -- sh -c 'apk add socat && watch -- "echo "show peers" | socat unix:/tmp/admin.sock -"'
```

You can live debug the haproxy stick-table via:
```
# remember to change the table name if you change it in the config
kubectl --context kind-kind logs -f haproxy-<REPLICA-NAME> -c haproxy -- sh -c 'apk add socat && watch -- "echo "show table back-session" | socat unix:/tmp/admin.sock -"'
```

### architecture
TODO

### TODO
- [ ] compose url string properly
- [x] dynamically generated user/password in initContainer for DataPlaneAPI basic auth cfg
- [x] DataPlaneAPI listen only on localhost
- [x] avoid crashing at the beginning?
- [ ] Lower logging levels on some less useful things
