# Goldmine setup random notes and useful commands

## GitHub

Checking out a PR:

```
git fetch origin pull/6/head

git checkout FETCH_HEAD
```

Use `gh` to exercise the GitHub API:

```
gh api   -H "Accept: application/vnd.github+json"   -H "X-GitHub-Api-Version: 2022-11-28"   /orgs/goldmine-build/packages/container/gold_ingestion/versions
```

## Pushk

Once a container is built and uploaded to the registry we can use
pushk to push the application to the cluster:

```
bazel run //kube/go/pushk:pushk -- --repo_dir=$HOME/k8s-config  --verbose  github-webhook
```

Presumes k8s-config was checked out using `gh` and thus we have perms to git
push.

## Ingress

```
kubectl get ingress

# Inspect a single ingress
kubectl describe ingress loki
```

## Loki

```
export LOKI_ADDR=https://loki.tail433733.ts.net/

# Follow one app:
logcli query  -f --no-labels  '{app="github-webhook"}'
```

## Bash

Combine stderr and stdout together into a pipe:

```
logcli help 2>&1 | less
```

Rough measure of disk performance:

```
dd bs=128K count=1k if=/dev/zero of=testfile oflag=dsync; rm testfile
```

## Tailscale

Switch to using a different tailnet:

```
sudo tailscale switch jcgregorio@github
```

## Bazel

```
bazel mod tidy

# Build and upload container
bazel run --stamp //ci/cmd/github_webhook:github_webhook_container_push

```

## Demo page

```
./demopage.sh golden/modules/dots-sk
```

## GCS PubSub notifications

```
gcloud storage buckets notifications create gs://goldmine-build-private --topic=goldmine-gold-data-files --event-types OBJECT_FINALIZE

gcloud storage buckets notifications list gs://goldmine-build-private

gcloud storage buckets notifications describe projects/_/buckets/goldmine-build-private/notificationConfigs/7
```

## Gold

Run gold-server locally:

```
go run ./golden/cmd/gold-server --config=$HOME/k8s-config/gold/gold-config/common.json5 
```

## Kubernetes

```
watch kubectl get pods -A
```

```
scp jcgregorio@goldmine-prime:/home/jcgregorio/.kube/config ~/.kube/config
```
