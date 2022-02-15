include make/bazel.mk

testgo:
	go test -test.short ./go/...

.PHONY: testjs
testjs:
	cd js && $(MAKE) test

.PHONY: sharedgo
sharedgo:
	cd go && $(MAKE) all

.PHONY: cq_watcher
cq_watcher:
	cd cq_watcher && $(MAKE) default

.PHONY: skolo
skolo:
	cd skolo && $(MAKE) all

# This target is invoked by the Infra-PerCommit-Build tryjob.
.PHONY: all
all: sharedgo cq_watcher build-frontend-ci

.PHONY: tags
tags:
	-rm tags
	find . -name "*.go" -print -or -name "*.js" -or -name "*.html" | xargs ctags --append

.PHONY: buildall
buildall:
	go build ./...

# Docker image used to run Puppeteer tests (Webpack build).
PUPPETEER_TESTS_DOCKER_IMG=gcr.io/skia-public/rbe-container-skia-infra:2021-10-05T13_35_51Z-kjlubick-6d6763c-dirty

# This is invoked from Infra-PerCommit-Puppeteer.
.PHONY: puppeteer-tests
puppeteer-tests:
	docker run --interactive --rm \
		--mount type=bind,source=`pwd`,target=/src \
		--mount type=bind,source=`pwd`/puppeteer-tests/output,target=/out \
		$(PUPPETEER_TESTS_DOCKER_IMG) \
		/src/puppeteer-tests/docker/run-tests.sh

# Front-end code will be built by the Infra-PerCommit-Build tryjob.
#
# All apps with a webpack.config.ts file should be included here.
.PHONY: build-frontend-ci
build-frontend-ci: npm-ci
	cd new_element && $(MAKE) build-frontend-ci

# Front-end tests will be included in the Infra-PerCommit-Medium tryjob.
#
# All apps with a karma.conf.ts file should be included here.
.PHONY: test-frontend-ci
test-frontend-ci: npm-ci
	cd new_element && $(MAKE) test-frontend-ci
	cd puppeteer-tests && $(MAKE) test-frontend-ci

.PHONY: update-go-bazel-files
update-go-bazel-files:
	$(BAZEL) run //:gazelle -- update ./

.PHONY: update-go-bazel-deps
update-go-bazel-deps:
	$(BAZEL) run //:gazelle -- update-repos -from_file=go.mod -to_macro=go_repositories.bzl%go_repositories

.PHONY: gazelle
gazelle: update-go-bazel-deps update-go-bazel-files

.PHONY: buildifier
buildifier:
	$(BAZEL) run //:buildifier

.PHONY: bazel-build
bazel-build:
	$(BAZEL) build //...

.PHONY: bazel-test
bazel-test:
	$(BAZEL) test //...

.PHONY: bazel-test-nocache
bazel-test-nocache:
	$(BAZEL) test --cache_test_results=no //...

.PHONY: bazel-build-rbe
bazel-build-rbe:
	$(BAZEL) build --config=remote //...

.PHONY: bazel-test-rbe
bazel-test-rbe:
	$(BAZEL) test --config=remote //...

.PHONY: bazel-test-rbe-nocache
bazel-test-rbe-nocache:
	$(BAZEL) test --config=remote --cache_test_results=no //...

.PHONY: eslint
eslint:
	-npx eslint --fix .

include make/npm.mk
