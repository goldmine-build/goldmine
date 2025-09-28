include ../make/bazel.mk

push:
	$(BAZEL) run --stamp //jsdoc:jsdoc_push
	# Update the image using kustomize. Note we need full kustomize, not the
	# subset packged in kubectl.
	echo kustomize edit set image "us-east1-docker.pkg.dev/api-project-146332866891/goldmine/jsdoc@*=*@`cat ../_bazel_bin/jsdoc/jsdoc_tag.txt`"

run-local:
	$(BAZEL) run //jsdoc:jsdoc_local
	docker run -ti -p 8000 jsdoc:latest
