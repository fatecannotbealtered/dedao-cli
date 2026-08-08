#!/usr/bin/env node
"use strict";

const fs = require("fs");
const path = require("path");
const zlib = require("zlib");
const { execFileSync } = require("child_process");

const root = path.resolve(__dirname, "..");
const rootPackage = require(path.join(root, "package.json"));
const toolName = Object.keys(rootPackage.bin || {})[0] || rootPackage.name.split("/").pop();
const version = rootPackage.version;
const inputDir = path.resolve(process.argv[2] || path.join(root, "dist"));
const outputDir = path.resolve(process.argv[3] || path.join(root, "npm-platform"));

const osMap = { darwin: "darwin", linux: "linux", win32: "windows" };
const archMap = { x64: "amd64", arm64: "arm64" };

function walk(dir) {
  const out = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...walk(p));
    else out.push(p);
  }
  return out;
}

function findArchive(npmOS, npmCPU) {
  const releaseOS = osMap[npmOS];
  let releaseArch = archMap[npmCPU];
  let archive = findArchiveExact(releaseOS, releaseArch);
  if (!archive && npmOS === "win32" && npmCPU === "arm64") {
    releaseArch = "amd64";
    archive = findArchiveExact(releaseOS, releaseArch);
  }
  if (!archive) {
    throw new Error(`No release archive found for npm platform ${npmOS}-${npmCPU}`);
  }
  return archive;
}

function findArchiveExact(releaseOS, releaseArch) {
  const base = `${toolName}-${version}-${releaseOS}-${releaseArch}`;
  return archives.find((f) => path.basename(f) === `${base}.tar.gz`) ||
    archives.find((f) => path.basename(f) === `${base}.zip`);
}

// extractZip reads one member out of a release zip using only what Node ships.
//
// It used to shell out to `unzip` or to Windows' bundled tar, which meant the
// packaging step could only be rehearsed on a machine that happened to have the
// right one -- and on Windows, whichever `tar` came first on PATH was usually
// GNU tar, which cannot read a zip at all. A release archive is a flat store or
// deflate zip, so reading it directly is both simpler and portable.
function extractZip(archive, binaryName, destBin) {
  const buffer = fs.readFileSync(archive);
  // The end-of-central-directory record is last, after a comment of unknown
  // length, so it is found by scanning backwards for its signature.
  let end = -1;
  for (let i = buffer.length - 22; i >= 0; i--) {
    if (buffer.readUInt32LE(i) === 0x06054b50) {
      end = i;
      break;
    }
  }
  if (end < 0) {
    throw new Error(`${path.basename(archive)} is not a zip archive`);
  }
  const count = buffer.readUInt16LE(end + 10);
  let offset = buffer.readUInt32LE(end + 16);

  for (let i = 0; i < count; i++) {
    if (buffer.readUInt32LE(offset) !== 0x02014b50) {
      throw new Error(`${path.basename(archive)} has a damaged central directory`);
    }
    const method = buffer.readUInt16LE(offset + 10);
    const compressedSize = buffer.readUInt32LE(offset + 20);
    const nameLength = buffer.readUInt16LE(offset + 28);
    const extraLength = buffer.readUInt16LE(offset + 30);
    const commentLength = buffer.readUInt16LE(offset + 32);
    const localOffset = buffer.readUInt32LE(offset + 42);
    const name = buffer.toString("utf8", offset + 46, offset + 46 + nameLength);

    if (name === binaryName || path.basename(name) === binaryName) {
      // The local header repeats the name and extra fields, and its lengths are
      // the authoritative ones for locating the data.
      const localNameLength = buffer.readUInt16LE(localOffset + 26);
      const localExtraLength = buffer.readUInt16LE(localOffset + 28);
      const start = localOffset + 30 + localNameLength + localExtraLength;
      const raw = buffer.subarray(start, start + compressedSize);
      const bytes = method === 0 ? raw : zlib.inflateRawSync(raw);
      fs.writeFileSync(destBin, bytes);
      return;
    }
    offset += 46 + nameLength + extraLength + commentLength;
  }
  throw new Error(`${path.basename(archive)} carries no ${binaryName}`);
}

function extractBinary(archive, destBin, npmOS) {
  fs.mkdirSync(path.dirname(destBin), { recursive: true });
  const binaryName = toolName + (npmOS === "win32" ? ".exe" : "");
  if (archive.endsWith(".zip")) {
    extractZip(archive, binaryName, destBin);
  } else {
    // Run from the destination and name the archive relative to it. An
    // absolute Windows path contains a colon, which GNU tar reads as a
    // remote-host spec ("C:" as a hostname); CI is Linux so this never broke a
    // release, but it made the packaging step impossible to rehearse locally.
    const destination = path.dirname(destBin);
    execFileSync("tar", ["-xzf", path.relative(destination, archive), binaryName], {
      cwd: destination,
      stdio: "ignore",
    });
  }
  if (npmOS !== "win32") fs.chmodSync(destBin, 0o755);
}

function platformParts(packageName) {
  const prefix = `${rootPackage.name}-`;
  if (!packageName.startsWith(prefix)) {
    throw new Error(`Optional dependency ${packageName} does not start with ${prefix}`);
  }
  const suffix = packageName.slice(prefix.length);
  const idx = suffix.indexOf("-");
  if (idx < 0) throw new Error(`Cannot parse npm platform package ${packageName}`);
  return { os: suffix.slice(0, idx), cpu: suffix.slice(idx + 1) };
}

fs.rmSync(outputDir, { recursive: true, force: true });
fs.mkdirSync(outputDir, { recursive: true });

if (!fs.existsSync(inputDir)) {
  throw new Error(`Input directory does not exist: ${inputDir}`);
}

const archives = walk(inputDir);

for (const packageName of Object.keys(rootPackage.optionalDependencies || {})) {
  const { os, cpu } = platformParts(packageName);
  const archive = findArchive(os, cpu);
  const packageDir = path.join(outputDir, packageName.replace(/^@/, "").replace("/", "__"));
  const binPath = path.join(packageDir, "bin", toolName + (os === "win32" ? ".exe" : ""));
  extractBinary(archive, binPath, os);
  const platformPackage = {
    name: packageName,
    version,
    description: `${toolName} prebuilt binary for ${os}-${cpu}`,
    license: rootPackage.license,
    author: rootPackage.author,
    homepage: rootPackage.homepage,
    repository: rootPackage.repository,
    publishConfig: { access: "public" },
    os: [os],
    cpu: [cpu],
    files: ["bin/"],
    engines: rootPackage.engines || { node: ">=16" }
  };
  fs.writeFileSync(path.join(packageDir, "package.json"), JSON.stringify(platformPackage, null, 2) + "\n");
  console.log(`Prepared ${packageName} from ${path.basename(archive)}`);
}
