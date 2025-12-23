include ../make/bazel.mk
include ../kube/kube.mk

.PHONY: default
default:
	bazel build cmd/...

.PHONY: frontend
frontend:
	# Makes sure all the frontend pages and tests compile.
	bazel build modules/...

.PHONY: goldpushk
goldpushk: kube-conf-gen
	go install ./cmd/goldpushk/...


# Build the gold_server container with Bazel and push it to the container registry.
.PHONY: release_gold_server
release_gold_server:
	$(BAZEL) run --stamp //golden:gold_server_push

# Build the gold-frontend container with Bazel and push it to GCR.
.PHONY: release_frontend_container
release_frontend_container:
	$(BAZEL) run --stamp //golden:gold_frontend_container_push

# Build the gold-ingestion container with Bazel and push it to GCR.
.PHONY: release_ingestion_container
release_ingestion_container:
	$(BAZEL) run --stamp //golden:gold_ingestion_container_push

# Build the gold-baseline-server container with Bazel and push it to GCR.
.PHONY: release_baseline_server_container
release_baseline_server_container:
	$(BAZEL) run --stamp //golden:baseline_server_container_push

# Build the gold-periodictasks container with Bazel and push it to GCR.
.PHONY: release_periodictasks_container
release_periodictasks_container:
	$(BAZEL) run --stamp //golden:periodictasks_container_push

# Build the gold-diffcalculator container with Bazel and push it to GCR.
.PHONY: release_diffcalculator_container
release_diffcalculator_container:
	$(BAZEL) run --stamp //golden:diffcalculator_container_push

# Must run before in a separate shell:  kubectl port-forward service/cockroachdb-public 26257:26257
#
# Also need to update common.json5 to update the address of the database to localhost.
run_local_ingestion:
	go run ./cmd/gold_ingestion --config=${HOME}/k8s-config/gold/gold-config/common.json5  



# These all assume the k8s-config file is checked out in $HOME and checked out
# using `gh` so that perms are setup to push.
build_and_push_ingestion: release_ingestion_container
	$(BAZEL) run //kube/go/pushk:pushk -- --repo_dir=${HOME}/k8s-config  --verbose  gold_ingestion 

.PHONY: mocks
mocks:
	go generate ./...
