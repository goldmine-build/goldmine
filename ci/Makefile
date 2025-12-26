include ../make/bazel.mk

release:
	$(BAZEL) run --stamp //ci/go/triggers/github:github_webhook_container_push


push:
	$(BAZEL) run --stamp //ci/go/triggers/github:github_webhook_container_push
	$(BAZEL) run //kube/go/pushk:pushk -- --repo_dir=${HOME}/k8s-config  --verbose  github-webhook
