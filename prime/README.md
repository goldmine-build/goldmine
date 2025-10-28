# Install minikube

```
apt install podman crun kubectl sudo
```

# Setup sudo for no password: ALL            ALL = (ALL) NOPASSWD: ALL

```
adduser jcgregorio sudo
curl -LO https://storage.googleapis.com/minikube/releases/latest/minikube_latest_amd64.deb
sudo dpkg -i minikube_latest_amd64.deb
```


$ cat /etc/systemd/system/minikube.service 

```
[Unit]
Description=minikube
After=network-online.target firewalld.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/home/jcgregorio
ExecStart=/usr/bin/minikube start
ExecStop=/usr/bin/minikube stop
User=jcgregorio
Group=jcgregorio

[Install]
WantedBy=multi-user.target
```

The make sure it starts on startup:

```
systemctl daemon-reload 
systemctl enable minikube
systemctl start minikube
```