#!/usr/bin/env bash
set -e
set -x
set -o pipefail 

bazel test //golden/modules/... //perf/modules/...

bazel run //gold-client/cmd/goldctl -- auth --service-account ~/Downloads/api-project-146332866891-3816d0d33259.json --work-dir $HOME/goldctl

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

# Loop over all files in the "to_upload" directory and move them to the "gold" directory.
for filename in $EXTRACT_DIR/*.png; do
  bazel run //gold-client/cmd/goldctl -- imgtest add --png-file $filename --test-name `echo $(basename $filename) | sed s/.*_// | sed s/.png//` --work-dir $WORKDIR
done

bazel run //gold-client/cmd/goldctl -- imgtest finalize --work-dir $HOME/goldctl
