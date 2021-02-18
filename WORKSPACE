workspace(
    name = "skia_infra",

    # Must be kept in sync with the npm_install rules defined below invoked below.
    managed_directories = {
        "@infra-sk_npm": ["infra-sk/node_modules"],
    },
)

load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

# We load bazel-toolchains here, rather than closer where it's first used (RBE container toolchain),
# because the grpc_deps() macro (invoked below) will pull an old version of bazel-toolchains if it's
# not already defined.
http_archive(
    name = "bazel_toolchains",
    sha256 = "1adf5db506a7e3c465a26988514cfc3971af6d5b3c2218925cd6e71ee443fc3f",
    strip_prefix = "bazel-toolchains-4.0.0",
    urls = [
        "https://github.com/bazelbuild/bazel-toolchains/releases/download/4.0.0/bazel-toolchains-4.0.0.tar.gz",
    ],
)

###############
# Buildifier. #
###############

http_archive(
    name = "com_github_bazelbuild_buildtools",
    sha256 = "2adaafee16c53b80adff742b88bc90b2a5e99bf6889a5d82f22ef66655dc467b",
    strip_prefix = "buildtools-4.0.0",
    url = "https://github.com/bazelbuild/buildtools/archive/4.0.0.zip",
)

##############################
# Go rules and dependencies. #
##############################

http_archive(
    name = "io_bazel_rules_go",
    sha256 = "6f111c57fd50baf5b8ee9d63024874dd2a014b069426156c55adbf6d3d22cb7b",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/rules_go/releases/download/v0.25.0/rules_go-v0.25.0.tar.gz",
        "https://github.com/bazelbuild/rules_go/releases/download/v0.25.0/rules_go-v0.25.0.tar.gz",
    ],
)

http_archive(
    name = "bazel_gazelle",
    sha256 = "b85f48fa105c4403326e9525ad2b2cc437babaa6e15a3fc0b1dbab0ab064bc7c",
    urls = [
        "https://mirror.bazel.build/github.com/bazelbuild/bazel-gazelle/releases/download/v0.22.2/bazel-gazelle-v0.22.2.tar.gz",
        "https://github.com/bazelbuild/bazel-gazelle/releases/download/v0.22.2/bazel-gazelle-v0.22.2.tar.gz",
    ],
)

load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies")
load("@io_bazel_rules_go//go:deps.bzl", "go_register_toolchains", "go_rules_dependencies")
load("//:go_repositories.bzl", "go_repositories")

# gazelle:repository_macro go_repositories.bzl%go_repositories
go_repositories()

go_rules_dependencies()

# Gazelle fails for toolchain versions < 1.14 with an error like the following:
#
#     gazelle: [...]: go: updates to go.mod needed, disabled by -mod=readonly
go_register_toolchains(version = "1.15.7")

gazelle_dependencies()

##########################
# Other Go dependencies. #
##########################

# Needed by @com_github_bazelbuild_remote_apis.
load("@com_github_bazelbuild_remote_apis//:repository_rules.bzl", "switched_rules_by_language")

switched_rules_by_language(
    name = "bazel_remote_apis_imports",
    go = True,
)

# Needed by @com_github_bazelbuild_remote_apis.
http_archive(
    name = "com_google_protobuf",
    sha256 = "9748c0d90e54ea09e5e75fb7fac16edce15d2028d4356f32211cfa3c0e956564",
    strip_prefix = "protobuf-3.11.4",
    urls = ["https://github.com/protocolbuffers/protobuf/archive/v3.11.4.zip"],
)

load("@com_google_protobuf//:protobuf_deps.bzl", "protobuf_deps")

protobuf_deps()

# Needed by @com_github_bazelbuild_remote_apis for the googleapis protos.
http_archive(
    name = "googleapis",
    build_file = "BUILD.googleapis",
    sha256 = "7b6ea252f0b8fb5cd722f45feb83e115b689909bbb6a393a873b6cbad4ceae1d",
    strip_prefix = "googleapis-143084a2624b6591ee1f9d23e7f5241856642f4d",
    urls = ["https://github.com/googleapis/googleapis/archive/143084a2624b6591ee1f9d23e7f5241856642f4d.zip"],
)

# Needed by @com_github_bazelbuild_remote_apis for gRPC.
http_archive(
    name = "com_github_grpc_grpc",
    sha256 = "b391a327429279f6f29b9ae7e5317cd80d5e9d49cc100e6d682221af73d984a6",
    strip_prefix = "grpc-93e8830070e9afcbaa992c75817009ee3f4b63a0",  # v1.24.3 with fixes
    urls = ["https://github.com/grpc/grpc/archive/93e8830070e9afcbaa992c75817009ee3f4b63a0.zip"],
)

load("@com_github_grpc_grpc//bazel:grpc_deps.bzl", "grpc_deps")

grpc_deps()

###################################################
# JavaScript / TypeScript rules and dependencies. #
###################################################

http_archive(
    name = "build_bazel_rules_nodejs",
    sha256 = "0f2de53628e848c1691e5729b515022f5a77369c76a09fbe55611e12731c90e3",
    urls = ["https://github.com/bazelbuild/rules_nodejs/releases/download/2.0.1/rules_nodejs-2.0.1.tar.gz"],
)

# The npm_install rule runs anytime the package.json or package-lock.json file changes. It also
# extracts any Bazel rules distributed in an npm package.
#
# There must be one npm_install rule for each package.json file in this repository. Any node_modules
# directories managed by npm_install rules must be mentioned in the workspace() rule at the top of
# this file.
load("@build_bazel_rules_nodejs//:index.bzl", "npm_install")

# Manages infra-sk's node_modules directory.
npm_install(
    name = "infra-sk_npm",
    package_json = "//infra-sk:package.json",
    package_lock_json = "//infra-sk:package-lock.json",
)

################################
# Sass rules and dependencies. #
################################

http_archive(
    name = "io_bazel_rules_sass",
    sha256 = "aa53d3d2a3313462dae5b357354e00d187f3bb659e994eb9b96a6033c4da2cc2",
    strip_prefix = "rules_sass-1.26.10",
    url = "https://github.com/bazelbuild/rules_sass/archive/1.26.10.zip",
)

load("@io_bazel_rules_sass//:package.bzl", "rules_sass_dependencies")

rules_sass_dependencies()

load("@io_bazel_rules_sass//:defs.bzl", "sass_repositories")

sass_repositories()

##################################
# Docker rules and dependencies. #
##################################

http_archive(
    name = "io_bazel_rules_docker",
    sha256 = "1698624e878b0607052ae6131aa216d45ebb63871ec497f26c67455b34119c80",
    strip_prefix = "rules_docker-0.15.0",
    urls = ["https://github.com/bazelbuild/rules_docker/releases/download/v0.15.0/rules_docker-v0.15.0.tar.gz"],
)

load(
    "@io_bazel_rules_docker//repositories:repositories.bzl",
    container_repositories = "repositories",
)

container_repositories()

# This is required by the toolchain_container rule.
load(
    "@io_bazel_rules_docker//repositories:go_repositories.bzl",
    container_go_deps = "go_deps",
)

container_go_deps()

load(
    "@io_bazel_rules_docker//container:container.bzl",
    "container_pull",
)

###########################
# Remote Build Execution. #
###########################

load("@bazel_toolchains//rules:rbe_repo.bzl", "rbe_autoconfig")

# Pulls the base container used to build the Skia Infrastructure custom RBE toolchain container.
container_pull(
    name = "rbe_ubuntu1604",
    digest = "sha256:f6568d8168b14aafd1b707019927a63c2d37113a03bcee188218f99bd0327ea1",
    registry = "gcr.io",
    repository = "cloud-marketplace/google/rbe-ubuntu16-04",
)

load("@bazel_toolchains//rules/exec_properties:exec_properties.bzl", "rbe_exec_properties")

# Defines a local repository named "exec_properties" which defines constants such as NETWORK_ON.
# See https://github.com/bazelbuild/bazel-toolchains/tree/master/rules/exec_properties.
rbe_exec_properties(
    name = "exec_properties",
)

load("@exec_properties//:constants.bzl", "NETWORK_ON")

rbe_autoconfig(
    name = "rbe_default",
    # Digest of the rbe-ubuntu16-04 image pulled above.
    base_container_digest = "sha256:f6568d8168b14aafd1b707019927a63c2d37113a03bcee188218f99bd0327ea1",
    # Digest of the most recent gcr.io/skia-public/rbe-container-skia-infra image.
    #
    # Must be updated manually after a new container image is uploaded to the container registry
    # via "bazel run //:push_rbe_container_skia_infra".
    digest = "sha256:78c203c69ccc6e2f78cd389b64d5302dd6b224f13c4fcb176bc9a16fb3c37910",
    # Enable networking. Without this, tests that require network access will fail. Examples include
    # go_test targets that try to clone the Skia Git repo from https://skia.googlesource.com/skia,
    # tests that hit GCS, etc.
    #
    # See https://github.com/bazelbuild/bazel-toolchains/tree/master/rules/exec_properties.
    #
    # Note that depending on network resources breaks test hermeticity.
    exec_properties = NETWORK_ON,
    registry = "gcr.io",
    repository = "skia-public/rbe-container-skia-infra",
    use_legacy_platform_definition = False,  # The exec_properties argument requires this.
)
