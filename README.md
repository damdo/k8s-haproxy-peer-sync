## k8s-haproxy-peer-sync

Create a cluster where to deploy it.
For the purpose of example we'll use a k8s kind cluster here:
```
kind create cluster --name "kind" --config cluster.yaml
```

TODO:
- [ ] compose url string properly
- [ ] dynamically generated user/password in initContainer for DataPlaneAPI basic auth cfg
- [x] DataPlaneAPI listen only on localhost
