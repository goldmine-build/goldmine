"""This module defines rules for building Skia Infrastructure web applications."""

load("@build_bazel_rules_nodejs//:index.bzl", "nodejs_test")
load("@infra-sk_npm//@bazel/typescript:index.bzl", "ts_library")
load("@infra-sk_npm//@bazel/rollup:index.bzl", "rollup_bundle")
load("@infra-sk_npm//@bazel/terser:index.bzl", "terser_minified")
load("@infra-sk_npm//html-insert-assets:index.bzl", "html_insert_assets")
load("@io_bazel_rules_sass//:defs.bzl", "sass_binary")
load("//infra-sk/html_insert_nonce_attribute:index.bzl", "html_insert_nonce_attribute")
load("//bazel/test_on_env:test_on_env.bzl", "test_on_env")

def nodejs_mocha_test(name, srcs = [], deps = [], tags = [], args = None, visibility = None):
    """Runs a NodeJS unit test using the Mocha test runner.

    For tests that should run in the browser, please use karma_mocha_test instead.

    Args:
      name: Name of the target.
      srcs: Labels for the test's TypeScript or JavaScript files.
      deps: Any ts_library dependencies.
      tags: Tags for the generated nodjs_test rule.
      args: Additional command-line arguments for the mocha test runner.
      visibility: Visibility of the generated nodejs_test rule.
    """
    if args == None:
        args = ["$(rootpath %s)" % src for src in srcs]

    nodejs_test(
        name = name,
        entry_point = "@infra-sk_npm//:node_modules/mocha/bin/mocha",
        data = srcs + deps + [
            "@infra-sk_npm//chai",
            "@infra-sk_npm//mocha",
            "@infra-sk_npm//ts-node",
            "@infra-sk_npm//@types/chai",
            "@infra-sk_npm//@types/mocha",
            "@infra-sk_npm//@types/node",
            "//:tsconfig.json",
        ],
        templated_args = [
            "--require ts-node/register",
            "--timeout 60000",
            "--colors",
        ] + args,
        tags = tags,
        visibility = visibility,
    )

def sk_element_puppeteer_test(name, srcs, sk_demo_page_server, deps = []):
    """Defines a Puppeteer test for the demo page served by an sk_demo_page_server.

    Puppeteer tests should save any screenshots inside the $TEST_UNDECLARED_OUTPUTS_DIR directory.
    To reduce the chances of name collisions, tests must save their screenshots under the
    $TEST_UNDECLARED_OUTPUTS_DIR/puppeteer-test-screenshots subdirectory. This convention will
    allow us to recover screenshots from multiple tests in a consistent way.

    Screenshots, and any other undeclared outputs of a test, can be found under //bazel-testlogs
    bundled as a single .zip file per test target. For example, if we run a Puppeteer test with e.g.
    "bazel test //path/to/my:puppeteer_test", any screenshots taken by this test will be found
    inside //bazel-testlogs/path/to/my/puppeteer_test/test.outputs/outputs.zip.

    To read more about undeclared test outputs, please see the following link:
    https://docs.bazel.build/versions/master/test-encyclopedia.html#test-interaction-with-the-filesystem.

    Args:
      name: Name of the rule.
      srcs: Labels for the test's TypeScript files.
      sk_demo_page_server: Label for the sk_demo_page_server target.
      deps: Any tss_library dependencies.
    """
    nodejs_mocha_test(
        name = name + "_test_only",
        srcs = srcs,
        tags = ["manual"],  # Exclude it from wildcards, e.g. "bazel test all".
        deps = deps + [
            "//puppeteer-tests:util_lib",
            "@infra-sk_npm//@types/puppeteer",
            "@infra-sk_npm//puppeteer",
        ],
    )

    test_on_env(
        name = name,
        env = sk_demo_page_server,
        test = name + "_test_only",
    )

def copy_file(name, src, dst):
    """Copies a single file to a destination path, making parent directories as needed."""
    native.genrule(
        name = name,
        srcs = [src],
        outs = [dst],
        cmd = "mkdir -p $$(dirname $@) && cp $< $@",
    )

def sk_page(name, deps, sass_deps, assets_serving_path = "/", nonce = None):
    """Builds a static HTML page, and its CSS and JavaScript development and production bundles.

    This macro takes a page name, e.g. "mypage", assumes the existence of files mypage.html,
    mypage.ts and mypage.scss, and defines the necessary build targets to generate the development
    and production bundles for said page.

    Input files:
      <name>.html
      <name>.ts
      <name>.scss

    Generated files:
      development/<name>.html
      development/<name>.ts
      development/<name>.scss
      production/<name>.html
      production/<name>.ts
      production/<name>.scss

    For convenience, a target with the same name as the "name" argument is defined, which generates
    all of the above files (e.g. bazel build //path/to:mypage).

    Tags <script> and <link> will be inserted into the output HTML pointing to the generated
    bundles. The serving path for said bundles defaults to "/" and can be overriden via the
    assets_serving_path argument.

    A timestamp will be appended to the URLs for any referenced assets for cache busting purposes,
    e.g. <script src="/index.js?v=27396986"></script>.

    If the nonce argument is provided, a nonce attribute will be inserted to all <link> and <script>
    tags. For example, if the nonce argument is set to "{% .Nonce %}", then the generated HTML will
    contain tags such as <script nonce="{% .Nonce %}" src="/index.js?v=27396986"></script>.

    This macro is designed to work side by side with the existing Webpack build without requiring
    any major changes to the pages in question.

    Args:
      name: The name used as a prefix for all the targets generated by this macro.
      deps: Any ts_library dependencies.
      sass_deps: Any sass_library dependencies.
      assets_serving_path: Path prefix for the inserted <script> and <link> tags.
      nonce: If set, its contents will be added as a "nonce" attributes to any inserted <script> and
        <link> tags.
    """

    # Output directories.
    DEV_OUT_DIR = "development"
    PROD_OUT_DIR = "production"

    #######################
    # JavaScript bundles. #
    #######################

    ts_library(
        name = "%s_ts_lib" % name,
        srcs = ["%s.ts" % name],
        deps = deps,
    )

    # Generates file <name>_js_bundle.js. Intermediate result; do not use.
    rollup_bundle(
        name = "%s_js_bundle" % name,
        deps = [
            ":%s_ts_lib" % name,
            "@infra-sk_npm//@rollup/plugin-node-resolve",
            "@infra-sk_npm//@rollup/plugin-commonjs",
            "@infra-sk_npm//rollup-plugin-sourcemaps",
        ],
        entry_point = "%s.ts" % name,
        format = "umd",
        config_file = "//infra-sk:rollup.config.js",
    )

    # Generates file <name>_js_bundle_minified.js. Intermediate result; do not use.
    terser_minified(
        name = "%s_js_bundle_minified" % name,
        src = "%s_js_bundle.js" % name,
        sourcemap = False,
    )

    # Generates file development/<name>.js.
    copy_file(
        name = "%s_js_dev" % name,
        src = "%s_js_bundle.js" % name,
        dst = "%s/%s.js" % (DEV_OUT_DIR, name),
    )

    # Generates file production/<name>.js.
    copy_file(
        name = "%s_js_prod" % name,
        # For some reason the output of the terser_minified rule above is not directly visible as a
        # source file, so we use the rule name instead (i.e. we drop the ".js" extension).
        src = "%s_js_bundle_minified" % name,
        dst = "%s/%s.js" % (PROD_OUT_DIR, name),
    )

    ################
    # CSS Bundles. #
    ################

    # Notes:
    #  - The source maps generated by the sass_binary rule are currently broken.
    #  - Sass compilation errors are not visible unless "bazel build" is invoked with flag
    #    "--strategy=SassCompiler=sandboxed". This is due to a known issue with sass_binary. For
    #    more details please see https://github.com/bazelbuild/rules_sass/issues/96.

    # Generates file development/<name>.css.
    sass_binary(
        name = "%s_css_dev" % name,
        src = "%s.scss" % name,
        output_name = "%s/%s.css" % (DEV_OUT_DIR, name),
        deps = sass_deps,
        include_paths = ["//infra-sk/node_modules"],
        output_style = "expanded",
        sourcemap = True,
    )

    # Generates file production/<name>.css.
    sass_binary(
        name = "%s_css_prod" % name,
        src = "%s.scss" % name,
        output_name = "%s/%s.css" % (PROD_OUT_DIR, name),
        deps = sass_deps,
        include_paths = ["//infra-sk/node_modules"],
        output_style = "compressed",
        sourcemap = False,
    )

    ###############
    # HTML files. #
    ###############

    # Generates file <name>.with_assets.html. Intermediate result; do not use.
    #
    # See https://www.npmjs.com/package/html-insert-assets.
    html_insert_assets(
        name = "%s_html" % name,
        outs = ["%s.with_assets.html" % name],
        args = [
            "--html=$(location %s.html)" % name,
            "--out=$@",
            "--roots=$(RULEDIR)",
            "--assets",
            # This is OK because html-insert-assets normalizes paths with successive slashes.
            "%s/%s.js" % (assets_serving_path, name),
            "%s/%s.css" % (assets_serving_path, name),
        ],
        data = ["%s.html" % name],
    )

    if nonce:
        # Generates file <name>.with_assets_and_nonce.html. Intermediate result; do not use.
        html_insert_nonce_attribute(
            name = "%s_html_nonce" % name,
            src = "%s.with_assets.html" % name,
            out = "%s.with_assets_and_nonce.html" % name,
            nonce = nonce,
        )

    instrumented_html = ("%s.with_assets_and_nonce.html" if nonce else "%s.with_assets.html") % name

    # Generates file development/<name>.html.
    copy_file(
        name = "%s_html_dev" % name,
        src = instrumented_html,
        dst = "%s/%s.html" % (DEV_OUT_DIR, name),
    )

    # Generates file production/<name>.html.
    copy_file(
        name = "%s_html_prod" % name,
        src = instrumented_html,
        dst = "%s/%s.html" % (PROD_OUT_DIR, name),
    )

    ###########################
    # Convenience filegroups. #
    ###########################

    # Generates all output files (that is, the development and production bundles).
    native.filegroup(
        name = name,
        srcs = [
            ":%s_dev" % name,
            ":%s_prod" % name,
        ],
    )

    # Generates the development bundle.
    native.filegroup(
        name = "%s_dev" % name,
        srcs = [
            "development/%s.html" % name,
            "development/%s.js" % name,
            "development/%s.css" % name,
        ],
    )

    # Generates the production bundle.
    native.filegroup(
        name = "%s_prod" % name,
        srcs = [
            "production/%s.html" % name,
            "production/%s.js" % name,
            "production/%s.css" % name,
        ],
    )
