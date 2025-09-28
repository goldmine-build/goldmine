include ../make/bazel.mk

push:
	$(BAZEL) run --stamp //jsdoc:jsdoc_push

run-local:
	$(BAZEL) run //jsdoc:jsdoc_local
	docker run -ti -p 8000 jsdoc:latest
