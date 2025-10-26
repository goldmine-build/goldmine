#!/usr/bin/env bash
set -e
set -x
set -o pipefail 

bazel test //golden/modules/... //perf/modules/...

bazel run //gold-client/cmd/goldctl -- auth --service-account ~/Downloads/api-project-146332866891-3816d0d33259.json --work-dir $HOME/goldctlx`

WORKDIR=$HOME/goldctl
EXTRACT_DIR=/tmp/gold
mkdir -p "$EXTRACT_DIR"

bazel run //gold-client/cmd/goldctl -- imgtest init \
  --bucket goldmine-build-private \
  --git_hash `git rev-parse HEAD` \
  --work-dir $HOME/goldctl \
  --upload-only \
  --corpus goldmine \
  --instance goldmine \
  --key browser:chrome \
  --key os:`uname -o` \
  --key machine:`uname -m`

bazel run //puppeteer-tests/bazel/extract_puppeteer_screenshots -- --output_dir=$EXTRACT_DIR

# Loop over all files in the "EXTRACT_DIR" directory and `imgtest add` them,
# which uploads the individual images to the cloud storage bucket, and also
# updates the local metadata in the WORKDIR.
for filename in $EXTRACT_DIR/*.png; do
  bazel run //gold-client/cmd/goldctl -- imgtest add --png-file $filename --test-name `echo $(basename $filename) | sed s/.*_// | sed s/.png//` --work-dir $WORKDIR
done

# Finalize the upload by uploading the metadata in the WORKDIR to the cloud
# storage bucket.
bazel run //gold-client/cmd/goldctl -- imgtest finalize --work-dir $HOME/goldctl
