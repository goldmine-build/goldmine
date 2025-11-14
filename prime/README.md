# Install minikube

```
apt install sudo
```

# Setup sudo for no password: ALL            ALL = (ALL) NOPASSWD: ALL

```
adduser jcgregorio sudo

# Then add NOPASSWD: to the %sudo line in /etc/sudoers file.
#
#     %sudo ALL=(ALL:ALL) NOPASSWD: ALL

curl -sfL https://get.k3s.io | sh -
sudo chmod 644 /etc/rancher/k3s/k3s.yaml
```

# Install Tailscale


Follow instruction based on distribution

https://pkgs.tailscale.com/stable/?v=1.86.2#debian-trixie


# Install CDB

    kubectl apply ./gold/cockroachcd-statefulset.yaml

Then run the following to do the initialization:

    kubectl apply ./gold/cluster-init.yaml


# Ports


| Service                         | Port Number   | URL/cmd
| --------------------------------| ------------- |-----------------------------------------------------------------------
| cockroachdb UI                  | 8001          | http://goldmine-prime:8001
| jsdoc                           | 8002          | http://goldmine-prime:8002
| prometheus                      | 8003          | http://goldmine-prime:8003
| alertmanager                    | 8004          | http://goldmine-prime:8004
| grafana                         | 8005          | http://goldmine-prime:8005
| gold-goldmine-ingestion         | 8006          | http://goldmine-prime:8006
| gold-goldmine-baseline_server   | 8007          | http://goldmine-prime:8007
| cockroachdb cli                 | 26257         | cockroach  sql --url postgres://root@goldmine-prime:26257/ --insecure

Run on k3s:

 - grafana
 - loki?
 - argocd?
 - gold (all components)
 - cdb-backup

