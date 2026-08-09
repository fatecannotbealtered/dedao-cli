#!/usr/bin/env node
"use strict";

const assert = require("assert").strict;
const fs = require("fs");
const os = require("os");
const path = require("path");
const {
  derivedLocations,
  hasReleasedHeading,
  newestReleased,
} = require("./version-files");
const { extractReleaseNotes } = require("./extract-release-notes");

const commentedOnly = [
  "# Changelog",
  "",
  "## [Unreleased]",
  "",
  "- Pending change.",
  "",
  "<!--",
  "## [1.2.3] - YYYY-MM-DD",
  "",
  "- Template text.",
  "-->",
  "",
].join("\n");

assert.equal(newestReleased(commentedOnly), null);
assert.equal(hasReleasedHeading(commentedOnly, "1.2.3"), false);
assert.throws(
  () => extractReleaseNotes(commentedOnly, "1.2.3"),
  /no visible CHANGELOG heading/
);

const root = fs.mkdtempSync(path.join(os.tmpdir(), "release-scripts-"));
try {
  fs.writeFileSync(path.join(root, "package.json"), '{"version":"1.2.3"}\n');
  fs.writeFileSync(path.join(root, "CHANGELOG.md"), commentedOnly);
  const descriptor = derivedLocations(root).find((item) => item.isChangelog);
  assert.ok(descriptor, "CHANGELOG descriptor is registered");
  assert.equal(descriptor.write("1.2.3"), 1);

  const updated = fs.readFileSync(path.join(root, "CHANGELOG.md"), "utf8");
  assert.equal(newestReleased(updated), "1.2.3");
  assert.equal(hasReleasedHeading(updated, "1.2.3"), true);
  const notes = extractReleaseNotes(updated, "1.2.3");
  assert.match(notes, /^## \[1\.2\.3\] - \d{4}-\d{2}-\d{2}$/m);
  assert.match(notes, /Pending change/);
  assert.doesNotMatch(notes, /Template text/);
} finally {
  fs.rmSync(root, { recursive: true, force: true });
}

console.log("release-scripts.test: OK");
