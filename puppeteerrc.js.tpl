/**
 * @type {import("puppeteer").Configuration}
 */
module.exports = {
  // Skip downloading and instead use a toolchain supplied browser.
  skipDownload: true,
  executablePath: "{BROWSER_EXE}",
  args: [
    '--disable-logging-colors', // No ASCII color highlights in logs.
  ]
};
