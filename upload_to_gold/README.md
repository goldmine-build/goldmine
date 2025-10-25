
First init authentication:

```
bazel run //gold-client/cmd/goldctl -- auth --service-account ~/Downloads/api-project-146332866891-3816d0d33259.json --work-dir $HOME/goldctl
```

Initialize for upload

```
bazel run //gold-client/cmd/goldctl -- imgtest init \
--bucket goldmine-build-private \
--git_hash `git rev-parse HEAD` \
--work-dir $HOME/goldctl \
--upload-only \
--corpus goldmine \
--instance goldmine \
--key browser:chrome \
--key os:`uname -o` \
--key machine:`uname -m` \
--key app:`echo $(basename $filename) | sed s/_.*//`
```


Extract all the rendered images.

```
bazel run //puppeteer-tests/bazel/extract_puppeteer_screenshots -- --output_dir=/tmp/gold  && ls /tmp/gold
```

For each image add it to the files to be uploaded:

```
filename=/tmp/gold/perf_commit-detail-picker-sk.png
bazel run //gold-client/cmd/goldctl -- imgtest add \
--png-file /tmp/gold/perf_commit-detail-picker-sk.png \
--test-name `echo $(basename $filename) | sed s/.*_// | sed s/.png//` 
```