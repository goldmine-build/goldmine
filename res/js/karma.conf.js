// Copyright (c) 2014 The Chromium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be found
// in the LICENSE file.
//
// Use of this source code is governed by a BSD-style
//  license that can be found in the LICENSE file.
//
// Karma configuration

module.exports = function(config) {
  config.set({

    // base path, that will be used to resolve files and exclude
    basePath: '',


    // frameworks to use
    frameworks: ['mocha', 'chai', 'sinon'],

    plugins: [
      'karma-chrome-launcher',
      'karma-firefox-launcher',
      'karma-sinon',
      'karma-mocha',
      'karma-chai'
    ],


    // list of files / patterns to load in the browser
    files: [
      'common.js',
      'tests/*.js',
      // Make import.html accessible, but don't include it using a <script> tag.
      {pattern: 'tests/import.html', included: false}
    ],


    // list of files to exclude
    exclude: [
    ],


    // test results reporter to use
    // possible values: 'dots', 'progress', 'junit', 'growl', 'coverage'
    reporters: ['dots'],


    // Get the port from KARMA_PORT if it is set.
    port: parseInt(process.env.KARMA_PORT || "9876"),


    // enable / disable colors in the output (reporters and logs)
    colors: false,


    // level of logging
    // possible values: config.LOG_DISABLE || config.LOG_ERROR ||
    // config.LOG_WARN || config.LOG_INFO || config.LOG_DEBUG
    logLevel: config.LOG_INFO,


    // enable / disable watching file and executing tests whenever any file changes
    autoWatch: true,


    // Start these browsers; we only care about Chrome for infra projects.
    browsers: ['Chrome'],


    // If browser does not capture in given timeout [ms], kill it
    captureTimeout: 60000,


    // Continuous Integration mode
    // if true, it capture browsers, run tests and exit
    //
    // This can be over-ridden by command-line flag when running Karma. I.e.:
    //
    //    ./node_modules/karma/bin/karma --no-single-run start
    //
    singleRun: true
  });
};
