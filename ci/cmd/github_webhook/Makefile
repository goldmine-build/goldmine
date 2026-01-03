default:
	bazel run --stamp //ci/cmd/github_webhook:github_webhook_container_push
	bazel run //kube/go/pushk:pushk -- --repo_dir=$(HOME)/k8s-config  --verbose  github-webhook