# Copyright (c) 2012 The Chromium Authors. All rights reserved.
# Use of this source code is governed by a BSD-style license that can be
# found in the LICENSE file.


"""Presubmit checks for the buildbot code."""


import subprocess


def _MakeFileFilter(input_api, include_extensions=None,
                    exclude_extensions=None):
  """Return a filter to pass to AffectedSourceFiles.

  The filter will include all files with a file extension in include_extensions,
  and will ignore all files with a file extension in exclude_extensions.

  If include_extensions is empty, all files, even those without any extension,
  are included.
  """
  white_list = [input_api.re.compile(r'.+')]
  if include_extensions:
    white_list = [input_api.re.compile(r'.+\.%s$' % ext)
                  for ext in include_extensions]
  # If black_list is empty, the InputApi default is used, so always include at
  # least one regexp.
  black_list = [input_api.re.compile(r'^$')]
  if exclude_extensions:
    black_list = [input_api.re.compile(r'.+\.%s$' % ext)
                  for ext in exclude_extensions]
  return lambda x: input_api.FilterSourceFile(x, white_list=white_list,
                                              black_list=black_list)

def _CheckNonAscii(input_api, output_api):
  """Check for non-ASCII characters and throw warnings if any are found."""
  results = []
  files_with_unicode_lines = []
  # We keep track of the longest file (in line count) so that we can pad the
  # numbers when displaying output. This makes it easier to see the indention.
  max_lines_in_any_file = 0
  FILE_EXTENSIONS = ['bat', 'cfg', 'cmd', 'conf', 'css', 'gyp', 'gypi', 'htm',
                     'html', 'js', 'json', 'ps1', 'py', 'sh', 'tac', 'yaml']
  file_filter = _MakeFileFilter(input_api, FILE_EXTENSIONS)
  for affected_file in input_api.AffectedSourceFiles(file_filter):
    affected_filepath = affected_file.LocalPath()
    unicode_lines = []
    with open(affected_filepath, 'r+b') as f:
      total_lines = 0
      for line in f:
        total_lines += 1
        try:
          line.decode('ascii')
        except UnicodeDecodeError:
          unicode_lines.append((total_lines, line.rstrip()))
    if unicode_lines:
      files_with_unicode_lines.append((affected_filepath, unicode_lines))
      if total_lines > max_lines_in_any_file:
        max_lines_in_any_file = total_lines

  if files_with_unicode_lines:
    padding = len(str(max_lines_in_any_file))
    long_text = 'The following files contain non-ASCII characters:\n'
    for filename, unicode_lines in files_with_unicode_lines:
      long_text += '  %s\n' % filename
      for line_num, line in unicode_lines:
        long_text += '    %s: %s\n' % (str(line_num).rjust(padding), line)
      long_text += '\n'
    results.append(output_api.PresubmitPromptWarning(
        message='Some files contain non-ASCII characters.',
        long_text=long_text))

  return results


def _CheckBannedGoAPIs(input_api, output_api):
  """Check go source code for functions and packages that should not be used."""
  # TODO(benjaminwagner): A textual search is easy, but it would be more
  #   accurate to parse and analyze the source due to package aliases.
  # A list of tuples of a regex to match an API and a suggested replacement for
  # that API.
  banned_replacements = [
    (r'\breflect\.DeepEqual\b', 'DeepEqual in go.skia.org/infra/go/testutils'),
    (r'\bgithub\.com/golang/glog\b', 'go.skia.org/infra/go/sklog'),
    (r'\bgithub\.com/skia-dev/glog\b', 'go.skia.org/infra/go/sklog'),
    (r'\bhttp\.Get\b', 'NewTimeoutClient in go.skia.org/infra/go/httputils'),
    (r'\bhttp\.Head\b', 'NewTimeoutClient in go.skia.org/infra/go/httputils'),
    (r'\bhttp\.Post\b', 'NewTimeoutClient in go.skia.org/infra/go/httputils'),
    (r'\bhttp\.PostForm\b',
        'NewTimeoutClient in go.skia.org/infra/go/httputils'),
    (r'\bos\.Interrupt\b', 'AtExit in go.skia.org/go/cleanup'),
    (r'\bsignal\.Notify\b', 'AtExit in go.skia.org/go/cleanup'),
    (r'\bsyscall.SIGINT\b', 'AtExit in go.skia.org/go/cleanup'),
    (r'\bsyscall.SIGTERM\b', 'AtExit in go.skia.org/go/cleanup'),
  ]

  compiled_replacements = []
  for (re, replacement) in banned_replacements:
    compiled_re = input_api.re.compile(re)
    compiled_replacements.append((compiled_re, replacement))

  errors = []
  file_filter = _MakeFileFilter(input_api, ['go'])
  for affected_file in input_api.AffectedSourceFiles(file_filter):
    affected_filepath = affected_file.LocalPath()
    for (line_num, line) in affected_file.ChangedContents():
      for (re, replacement) in compiled_replacements:
        match = re.search(line)
        if match:
          errors.append('%s:%s: Instead of %s, please use %s.' % (
              affected_filepath, line_num, match.group(), replacement))

  if errors:
    return [output_api.PresubmitPromptWarning('\n'.join(errors))]

  return []


def CheckChange(input_api, output_api):
  """Presubmit checks for the change on upload or commit.

  The presubmit checks have been handpicked from the list of canned checks
  here:
  https://chromium.googlesource.com/chromium/tools/depot_tools/+/master/presubmit_canned_checks.py

  The following are the presubmit checks:
  * Pylint is run if the change contains any .py files.
  * Enforces max length for all lines is 100.
  * Checks that the user didn't add TODO(name) without an owner.
  * Checks that there is no stray whitespace at source lines end.
  * Checks that there are no tab characters in any of the text files.
  """
  results = []

  pylint_blacklist = [
      r'infra[\\\/]bots[\\\/]recipes.py',
      r'.*[\\\/]\.recipe_deps[\\\/].*',
      r'.*[\\\/]node_modules[\\\/].*',
  ]
  pylint_blacklist.extend(input_api.DEFAULT_BLACK_LIST)
  pylint_disabled_warnings = (
      'F0401',  # Unable to import.
      'E0611',  # No name in module.
      'W0232',  # Class has no __init__ method.
      'E1002',  # Use of super on an old style class.
      'W0403',  # Relative import used.
      'R0201',  # Method could be a function.
      'E1003',  # Using class name in super.
      'W0613',  # Unused argument.
  )
  results += input_api.canned_checks.RunPylint(
      input_api, output_api,
      disabled_warnings=pylint_disabled_warnings,
      black_list=pylint_blacklist)

  # Use 100 for max length for files other than python. Python length is
  # already checked during the Pylint above. No max length for Go files.
  IGNORE_LINE_LENGTH = ['go', 'html', 'py']
  file_filter = _MakeFileFilter(input_api,
                                exclude_extensions=IGNORE_LINE_LENGTH)
  results += input_api.canned_checks.CheckLongLines(input_api, output_api, 100,
      source_file_filter=file_filter)

  file_filter = _MakeFileFilter(input_api)
  results += input_api.canned_checks.CheckChangeTodoHasOwner(
      input_api, output_api, source_file_filter=file_filter)
  results += input_api.canned_checks.CheckChangeHasNoStrayWhitespace(
      input_api, output_api, source_file_filter=file_filter)

  # CheckChangeHasNoTabs automatically ignores makefiles.
  IGNORE_TABS = ['go']
  file_filter = _MakeFileFilter(input_api, exclude_extensions=IGNORE_TABS)
  results += input_api.canned_checks.CheckChangeHasNoTabs(input_api, output_api)

  results += _CheckBannedGoAPIs(input_api, output_api)

  return results


def CheckChangeOnUpload(input_api, output_api):
  results = CheckChange(input_api, output_api)
  # Give warnings for non-ASCII characters on upload but not commit, since they
  # may be intentional.
  results.extend(_CheckNonAscii(input_api, output_api))
  return results


def CheckChangeOnCommit(input_api, output_api):
  results = CheckChange(input_api, output_api)
  return results
