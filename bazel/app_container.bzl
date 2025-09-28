"""This module defines the app_container macro."""

load("@aspect_bazel_lib//lib:expand_template.bzl", "expand_template")
load("@rules_oci//oci:defs.bzl", "oci_image", "oci_load", "oci_push")
load("@rules_pkg//:pkg.bzl", "pkg_tar")

def _app_container_impl(name, repository, base, exe, config, entrypoint, cmd, pages, env = None, **_kwargs):
    """
    Replacement for sk_app_container.
    """
    name_exe = name + "_exe"
    pkg_tar(
        name = name_exe,
        srcs = [exe],
        mode = "755",
        package_dir = "/usr/local/bin/",
    )

    pkg_tars = [":" + name_exe]

    if config:
        name_config = name + "_config"
        pkg_tar(
            name = name_config,
            srcs = [":configs"],
            mode = "644",
            package_dir = "/usr/local/share/{}/configs".format(name),
        )
        pkg_tars.append(":" + name_config)

    if pages:
        name_dist = name + "_dist"
        pkg_tar(
            name = name_dist,
            srcs = [page + "_prod" for page in pages],
            mode = "644",
            package_dir = "/usr/local/share/{}/dist".format(name),
        )
        pkg_tars.append(":" + name_dist)

    # Note that this macro also produces a target, //perf:perfserver.digest, that
    # generates the file `_bazel_bin/perf/perfserver.json.sha256` that contains the
    # sha256 of the image.
    name_image = name + "_image"
    oci_image(
        name = name_image,
        base = base,
        entrypoint = [entrypoint],
        cmd = cmd,
        env = env,
        # Link the resulting image back to the repository where the build is defined.
        #labels = labels,
        tars = pkg_tars,
        visibility = ["//visibility:public"],
    )

    # Use this target to build and load the container image into the local image
    # registry from where it can be run.
    #
    # For example:
    #
    #    $ bazel run //perf:local_perfserver
    #    $ docker run -ti perfserver:latest
    oci_load(
        name = name + "_local",
        image = name_image,
        repo_tags = [name + ":latest"],
        visibility = ["//visibility:public"],
    )

    name_stamped = name + "_stamped"
    expand_template(
        name = name_stamped,
        out = name + "_tag.txt",
        stamp_substitutions = {"0.0.0": "{{STABLE_DOCKER_TAG}}"},
        template = [
            "0.0.0",
        ],
    )

    oci_push(
        name = name + "_push",
        image = name_image,
        remote_tags = ":" + name_stamped,
        repository = repository,
        visibility = ["//visibility:public"],
    )

# app_container builds a container image that can be run locally or can be
# pushed to a registry.
app_container = macro(
    doc = """
app_container builds a container image that can be run locally or can be
pushed to a registry.

This macro produces the following targets:

    <name>_local - Use this target to build and load the 
        container image into the local image
        registry from where it can be run.
        
        For example:

            $ bazel run //perf:perfserver_local
            $ docker run -ti perfserver:latest

    <name>_push - Use this target to push the container
        image to the specified repository.

        If you want the image in the repository to have be tagged with
        the value of the STABLE_DOCKER_TAG, then you need to run the
        target with the --stamp flag, e.g.:

            bazel run --stamp //jsdoc:jsdoc_push


    <name>_image.digest - This target is built by
        the _local and _push targets is a file that contains
        the sha256 hash of the image.

    """,
    attrs = {
        "repository": attr.string(
            configurable = False,
            doc = """
            Repository URL where the image will be signed at, e.g.: `index.docker.io/<user>/image`.
            Digests and tags are not allowed.
            """,
            mandatory = True,
        ),
        "base": attr.label(
            configurable = False,
            allow_single_file = True,
            doc = """
            Label to an oci_image target to use as the base.
            """,
            mandatory = True,
        ),
        "exe": attr.label(
            configurable = False,
            allow_single_file = True,
            doc = """
            Label to an executable target, i.e. the application that will run in the container.
            """,
            mandatory = True,
        ),
        "config": attr.label(
            configurable = False,
            allow_single_file = True,
            doc = """
            Label to configuration files.
            """,
        ),
        "entrypoint": attr.string(
            configurable = False,
            doc = """
            Command to run by default on container startup.
            """,
        ),
        "cmd": attr.string_list(
            configurable = False,
            doc = """
            A file containing a newline separated list to be used as 
            the `command & args` of the container. These values act as defaults 
            and may be replaced by any specified when creating a container.
            """,
        ),
        "pages": attr.string_list(
            configurable = False,
            doc = """
            List of sk_pages to add to the image as a layer.
            """,
        ),
        "env": attr.label(
            configurable = False,
            allow_single_file = True,
            doc = """
            A file containing the default values for the environment variables of the container. These values act as defaults and are merged with any specified when creating a container. Entries replace the base environment variables if any of the entries has conflicting keys.
            To merge entries with keys specified in the base, `${KEY}` or `$KEY` syntax may be used.
            """,
        ),
    },
    implementation = _app_container_impl,
    finalizer = True,
)
