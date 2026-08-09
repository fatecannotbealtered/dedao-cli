#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");
const { stripHTMLComments } = require("./version-files");

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function extractReleaseNotes(markdown, version) {
  const normalized = String(version || "").trim();
  if (!normalized) throw new Error("release version is required");

  const visible = stripHTMLComments(markdown);
  const heading = new RegExp(
    `^## \\[${escapeRegExp(normalized)}\\](?:[ \\t]+-[^\\r\\n]+)?[ \\t]*$`,
    "m"
  );
  const match = heading.exec(visible);
  if (!match) throw new Error(`no visible CHANGELOG heading for ${normalized}`);

  const nextHeading = /^## \[[^\]]+\].*$/gm;
  nextHeading.lastIndex = match.index + match[0].length;
  const next = nextHeading.exec(visible);
  const section = visible.slice(match.index, next ? next.index : visible.length).trim();
  const body = section.slice(match[0].length).trim();
  if (!body) throw new Error(`CHANGELOG heading for ${normalized} has no release notes`);
  return section + "\n";
}

function main(args) {
  if (args.length !== 2) {
    throw new Error("usage: extract-release-notes.js <version> <output-file>");
  }
  const [version, outputFile] = args;
  const root = path.resolve(__dirname, "..");
  const markdown = fs.readFileSync(path.join(root, "CHANGELOG.md"), "utf8");
  const notes = extractReleaseNotes(markdown, version);
  fs.writeFileSync(path.resolve(process.cwd(), outputFile), notes);
}

if (require.main === module) {
  try {
    main(process.argv.slice(2));
  } catch (error) {
    console.error(`extract-release-notes: ${error.message}`);
    process.exitCode = 1;
  }
}

module.exports = { extractReleaseNotes };
