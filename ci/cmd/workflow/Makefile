build_and_push:
	bazel run --stamp //ci/cmd/workflow:ci_worker_container_push
	bazel run //kube/go/pushk:pushk -- --repo_dir=$(HOME)/k8s-config  --verbose  ci_workflow_server