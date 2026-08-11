#!/usr/bin/env node
// live-smoke.js — run every declared read command against the real service and
// check that what comes back matches what `reference` promises.
//
// This is the layer the mock tests cannot reach. Mock upstream answers with
// shapes this repo wrote itself, so it proves the envelope, the error mapping,
// and the exit codes -- but it can only ever confirm our own assumptions. The
// first live run of this tool found three defects that had survived every mock
// test: a pagination flag declared under the wrong name, a field declared that
// the service never sends, and a payload that invited a reader to report the
// publisher's words as the user's.
//
// It is deliberately NOT in CI. There is no sandbox and no test account tier;
// the only environment is the live service read with a real signed-in account
// (docs/E2E.md). Run it by hand before a release, and keep the report.
//
// Read-only. It never purchases, comments, follows, changes progress, or writes
// a note. Commands that mutate, need a human, or replace the binary are skipped
// by name and the skip is printed -- a silent omission would read as coverage.
//
// The write commands are the exception, and they are opt-in. `--include-writes`
// runs the full GetNote mutation chain against one disposable note created for
// that run and deleted before it ends (docs/E2E.md). It is not the default:
// nobody should have their notes written to because they ran a smoke test. A
// release candidate needs one run with it, because a read-only smoke cannot say
// anything about the commands that change a person's data.
//
// Usage:
//   node scripts/live-smoke.js                  # run, print a table, write the report
//   node scripts/live-smoke.js --bin ./dedao-cli
//   node scripts/live-smoke.js --report out.json
//   node scripts/live-smoke.js --json           # machine-readable summary on stdout
//   node scripts/live-smoke.js --include-writes # also exercise the GetNote write chain
//
// Exit codes:
//   0  every command that ran matched its declared schema
//   1  at least one command returned a field it does not declare, or failed
//   2  the tool could not be run, or no session is loaded

"use strict";
const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const TOOL = "dedao-cli";

// Commands this smoke must not run, and why. Named individually so that adding
// a command forces a decision rather than inheriting a silent exclusion.
const SKIP = {
  login: "starts a human-in-the-loop authorization",
  "login-resume": "settles a human-in-the-loop authorization",
  logout: "deletes local credentials",
  update: "replaces the running binary",
  "getnote auth login": "writes credentials",
  "getnote auth logout": "deletes credentials",
  "getnote save": "creates a note upstream",
  "getnote note update": "mutates a note upstream",
  "getnote note delete": "deletes a note upstream",
  "getnote note share": "publishes a public link",
  "getnote tag add": "mutates a note upstream",
  "getnote tag remove": "mutates a note upstream",
  "getnote kb create": "creates a knowledge base upstream",
  "getnote kb add": "mutates a knowledge base upstream",
  "getnote kb remove": "mutates a knowledge base upstream",
  "audiobook-media": "downloads a media file to disk",
  "getnote task": "needs a task id from a write this smoke does not perform",
};

function arg(name, fallback) {
  const index = process.argv.indexOf(name);
  return index >= 0 && process.argv[index + 1] ? process.argv[index + 1] : fallback;
}
const flag = (name) => process.argv.includes(name);

const bin = arg("--bin", TOOL);
const reportPath = arg("--report", path.join("live-smoke-report.json"));
const asJSON = flag("--json");
const includeWrites = flag("--include-writes");

function run(args) {
  const result = spawnSync(bin, [...args, "--compact"], {
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  });
  if (result.error) {
    return { spawnFailed: String(result.error.message) };
  }
  let envelope = null;
  try {
    envelope = JSON.parse(result.stdout);
  } catch {
    return { exit: result.status, parseFailed: true, raw: (result.stdout || "").slice(0, 200) };
  }
  return { exit: result.status, envelope };
}

// ---- discovery -------------------------------------------------------------

const reference = run(["reference"]);
if (reference.spawnFailed || !reference.envelope || !reference.envelope.ok) {
  console.error(`live-smoke: could not read \`${bin} reference\`.`);
  if (reference.spawnFailed) console.error(`  ${reference.spawnFailed}`);
  process.exit(2);
}
const ref = reference.envelope.data;
const schemas = ref.schemas || {};

const leaves = [];
(function walk(commands, prefix) {
  for (const command of commands || []) {
    const name = (prefix ? `${prefix} ` : "") + command.name;
    if (command.children && command.children.length) {
      walk(command.children, name);
    } else {
      leaves.push({ name, schema: command.output_schema, params: command.params || [] });
    }
  }
})(ref.commands, "");

const status = run(["status"]);
const signedIn = Boolean(status.envelope && status.envelope.ok && status.envelope.data.authenticated);
if (!signedIn) {
  console.error("live-smoke: no Dedao session. Run `dedao-cli login` first.");
  process.exit(2);
}
const notesAuthorized = Boolean(
  status.envelope.data.getnote && status.envelope.data.getnote.configured
);

// ---- identifier harvest ----------------------------------------------------
//
// Opaque ids are read from a listing, never guessed: an id invented here would
// test the error path and quietly report it as coverage.

function firstItem(envelope, ...keys) {
  if (!envelope || !envelope.ok) return null;
  for (const key of keys) {
    const list = envelope.data[key];
    if (Array.isArray(list) && list.length) return list[0];
  }
  return null;
}

const ids = {};
const courses = run(["library", "course", "--page-size", "3"]);
const course = firstItem(courses.envelope, "list");
if (course) ids.course = course.enid;
if (ids.course) {
  const articles = run(["articles", ids.course, "--count", "3"]);
  const article = firstItem(articles.envelope, "article_list", "list");
  if (article) ids.article = article.enid;
}
const ebook = firstItem(run(["library", "ebook", "--page-size", "3"]).envelope, "list");
if (ebook) ids.ebook = ebook.enid;
const audiobook = firstItem(run(["library", "audiobook", "--page-size", "3"]).envelope, "list");
if (audiobook) ids.audiobook = audiobook.enid;
if (notesAuthorized) {
  const note = firstItem(run(["getnote", "notes", "--limit", "3"]).envelope, "items");
  if (note) ids.getnote = note.note_id || note.id;
  const kb = firstItem(run(["getnote", "kbs"]).envelope, "items");
  if (kb) ids.kb = kb.id || kb.kb_id || kb.topic_id;
}
const label = firstItem(run(["labels", "1"]).envelope, "list");
if (label && label.enid) ids.label = label.enid;

// The ebook and audiobook commands read a detail record, and a detail record
// does not require owning the thing: the service answers with the metadata and
// an entitlement flag, which is exactly the boundary this tool exists to report.
// So their identifiers are harvested from public listings rather than from the
// library, and an account that owns neither still exercises both surfaces.
const ebookHit = firstItem(run(["search-type", "ebook-chapter", "认知"]).envelope, "list");
if (ebookHit && ebookHit.detail && ebookHit.detail.enid) ids.ebook = ebookHit.detail.enid;

// Not every audiobook label lists products that carry a readable enid, so the
// candidates are walked until one does rather than trusting the first.
const audiobookLabels = ((run(["labels", "1"]).envelope || { data: {} }).data.list || []).filter(
  (entry) => typeof entry.enid === "string" && entry.enid.startsWith("pkgGroupType:")
);
// Not every listed product's enid is an audiobook topic id, so a candidate is
// only accepted once the detail endpoint has actually answered for it. Trusting
// the first one made the smoke report E_VALIDATION as a tool defect when the
// real fault was the identifier this script had picked.
outer: for (const candidate of audiobookLabels) {
  const listed = run(["label-content", candidate.enid, "1", "1"]);
  const products = (listed.envelope && listed.envelope.ok && listed.envelope.data.product_list) || [];
  for (const product of products.slice(0, 3)) {
    if (product.en_package_id && !ids.audiobookCollection) {
      ids.audiobookCollection = product.en_package_id;
    }
    if (!product.product_enid) continue;
    const detail = run(["audiobook", product.product_enid]);
    if (!detail.envelope || !detail.envelope.ok) continue;
    ids.audiobook = product.product_enid;
    // The detail names the track its alias endpoint reads.
    const record = detail.envelope.data.detail || {};
    if (record.audio_id) ids.audiobookAlias = record.audio_id;
    const agency = record.agency_detail || {};
    if (agency.id && agency.id !== "0") ids.audiobookAgency = String(agency.id);
    break outer;
  }
}
// `library-groups` answers with a single group descriptor rather than a list,
// so the id is read off the object itself.
const groups = run(["library-groups", "course"]);
if (groups.envelope && groups.envelope.ok && groups.envelope.data.id) {
  ids.group = String(groups.envelope.data.id);
}

// ---- argument construction -------------------------------------------------
//
// Only commands whose required arguments can be satisfied from harvested ids
// are run. Anything else is reported as not-covered, with the reason.

function argsFor(leaf) {
  const required = leaf.params.filter((p) => p.required).map((p) => p.name);
  const table = {
    library: ["course"],
    "library-groups": ["course"],
    labels: ["1"],
    discover: ["1"],
    "search-type": ["course", "认知"],
    search: ["认知"],
    "search-suggest": ["认知"],
    course: ids.course && [ids.course],
    articles: ids.course && [ids.course, "--count", "3"],
    article: ids.article && [ids.article],
    "article-notes": ids.article && [ids.article],
    "article-captions": ids.article && [ids.article],
    comments: ids.article && [ids.article],
    ebook: ids.ebook && [ids.ebook],
    "ebook-chapters": ids.ebook && [ids.ebook],
    "ebook-community": ids.ebook && [ids.ebook],
    // An unowned book answers E_FORBIDDEN here, which is the honest answer and
    // is recorded as one -- the entitlement path is worth exercising too.
    "ebook-read": ids.ebook && [ids.ebook, "--chapter", "Chapter_1_1"],
    audiobook: ids.audiobook && [ids.audiobook],
    "audiobook-collection": ids.audiobookCollection && [ids.audiobookCollection],
    "audiobook-alias": ids.audiobookAlias && [ids.audiobookAlias],
    "audiobook-agency": ids.audiobookAgency && [ids.audiobookAgency],
    "getnote notes": ["--limit", "3"],
    "getnote note get": ids.getnote && [ids.getnote],
    "getnote tag list": ids.getnote && [ids.getnote],
    "getnote kb notes": ids.kb && [ids.kb],
    "getnote search": ["认知"],
    "label-content": ids.label && [ids.label, "1", "1"],
    "library-group": ids.group && ["course", ids.group],
  };
  if (Object.prototype.hasOwnProperty.call(table, leaf.name)) {
    return table[leaf.name] || null;
  }
  return required.length ? null : [];
}

// ---- the write chain -------------------------------------------------------
//
// One disposable note, created for this run and deleted before it ends. Every
// mutation goes through the two-step gate the tool requires, so this exercises
// the confirmation path as well as the write itself.

// confirmedWrite performs the dry-run/confirm handshake for one mutation.
function confirmedWrite(command, extra = []) {
  const preview = run([...command, ...extra, "--dry-run"]);
  if (!preview.envelope || !preview.envelope.ok) {
    // The dry-run's own code has to survive: swallowing it as "dry-run failed"
    // hid a rate limit once, and the cleanup that depended on it gave up and
    // left a disposable note in the account.
    const code = (preview.envelope && preview.envelope.error && preview.envelope.error.code) || "dry-run failed";
    return { ok: false, reason: code, envelope: preview.envelope };
  }
  const token = preview.envelope.data && preview.envelope.data.confirm_token;
  if (!token) return { ok: false, reason: "dry-run returned no confirm token" };
  const applied = run([...command, ...extra, "--confirm", token]);
  if (!applied.envelope || !applied.envelope.ok) {
    return {
      ok: false,
      reason: (applied.envelope && applied.envelope.error && applied.envelope.error.code) || "no envelope",
      envelope: applied.envelope,
    };
  }
  return { ok: true, envelope: applied.envelope };
}

// sleepSeconds blocks between retries. The service throttles bursts of writes,
// so pacing is part of exercising them rather than an optimisation.
function sleepSeconds(seconds) {
  spawnSync(process.execPath, ["-e", `setTimeout(()=>{}, ${seconds * 1000})`], {
    encoding: "utf8",
  });
}

const writeResults = new Map();
function recordWrite(command, outcome) {
  if (outcome.ok) {
    writeResults.set(command, { command, status: "write_ok" });
    return;
  }
  // Being throttled is the service's answer, and surfacing it is correct
  // behaviour -- the same rule the read side uses. It is recorded, not counted
  // as a broken write path.
  const throttled = outcome.reason === "E_RATE_LIMITED";
  writeResults.set(command, {
    command,
    status: throttled ? "answered" : "write_failed",
    ...(throttled ? { error_code: outcome.reason } : { reason: outcome.reason }),
  });
}

if (includeWrites && notesAuthorized) {
  // The marker names the run in the user's own notes, so anything this leaves
  // behind is identifiable rather than mysterious.
  const marker = `live-smoke disposable ${new Date().toISOString()}`;
  let noteID = null;
  try {
    const saved = confirmedWrite(["getnote", "save"], [
      "--title", marker, "--content", "Created by live-smoke; deleted at the end of the run.",
      "--note-type", "plain_text", "--wait",
    ]);
    recordWrite("getnote save", saved);
    if (saved.ok) {
      // Every mutation answers under `result` (schema `getnote_mutation`), so
      // that is where the new note's id is. Reading it from the top level
      // instead left a disposable note behind on the first run of this chain.
      const result = (saved.envelope.data || {}).result || {};
      noteID = result.note_id || result.id || null;
      const taskID =
        result.task_id || (Array.isArray(result.tasks) && result.tasks[0] && result.tasks[0].task_id);
      if (taskID) {
        const task = run(["getnote", "task", String(taskID)]);
        recordWrite("getnote task", { ok: Boolean(task.envelope && task.envelope.ok), reason: "task lookup" });
      }
    }

    if (noteID) {
      recordWrite("getnote note update",
        confirmedWrite(["getnote", "note", "update", noteID], ["--title", `${marker} (updated)`]));
      recordWrite("getnote note share", confirmedWrite(["getnote", "note", "share", noteID]));

      const tagName = "live-smoke";
      recordWrite("getnote tag add",
        confirmedWrite(["getnote", "tag", "add", noteID], ["--tag", tagName]));
      const tags = run(["getnote", "tag", "list", noteID]);
      const added =
        tags.envelope && tags.envelope.ok
          ? (tags.envelope.data.tags || []).find((tag) => tag.name === tagName)
          : null;
      if (added) {
        recordWrite("getnote tag remove",
          confirmedWrite(["getnote", "tag", "remove", noteID, String(added.id)]));
      }

      // `kb create` is deliberately not exercised: GetNote has no command to
      // delete a knowledge base, so creating one would leave residue in the
      // account that this run cannot clean up. add/remove run against a
      // knowledge base that already exists.
      if (ids.kb) {
        recordWrite("getnote kb add",
          confirmedWrite(["getnote", "kb", "add"], ["--note-id", noteID, "--topic-id", ids.kb]));
        recordWrite("getnote kb remove",
          confirmedWrite(["getnote", "kb", "remove"], ["--note-id", noteID, "--topic-id", ids.kb]));
      }
    }
  } finally {
    // The disposable note goes even if a step above threw: leaving it behind
    // would put test data in someone's notes permanently. If the id could not
    // be read, the note is found by the marker it was created with -- the first
    // run of this chain orphaned one exactly that way.
    if (!noteID) {
      const recent = run(["getnote", "notes", "--limit", "10"]);
      if (recent.envelope && recent.envelope.ok) {
        const orphan = (recent.envelope.data.items || []).find(
          (item) => typeof item.title === "string" && item.title.startsWith(marker)
        );
        if (orphan) noteID = orphan.note_id || orphan.id || null;
      }
    }
    if (noteID) {
      // Cleanup does not get to give up on a transient failure. Being throttled
      // after a burst of writes is the expected case, not an exception, and
      // "the smoke left test data in your notes" is not an acceptable outcome.
      let deleted = confirmedWrite(["getnote", "note", "delete", noteID]);
      for (let attempt = 0; !deleted.ok && attempt < 4; attempt += 1) {
        sleepSeconds(5 * (attempt + 1));
        deleted = confirmedWrite(["getnote", "note", "delete", noteID]);
      }
      recordWrite("getnote note delete", deleted);
      if (!deleted.ok) {
        console.error(
          `live-smoke: could not delete the disposable note ${noteID} (${deleted.reason}). ` +
            "Remove it by hand."
        );
      }
    }
  }
}

// ---- the run ---------------------------------------------------------------

const results = [];
for (const leaf of leaves) {
  if (writeResults.has(leaf.name)) {
    results.push(writeResults.get(leaf.name));
    continue;
  }
  if (SKIP[leaf.name]) {
    const reason =
      includeWrites && leaf.name === "getnote kb create"
        ? "GetNote has no command to delete a knowledge base, so this run cannot clean up after it"
        : SKIP[leaf.name];
    results.push({ command: leaf.name, status: "skipped", reason });
    continue;
  }
  if (leaf.name.startsWith("getnote ") && !notesAuthorized) {
    results.push({ command: leaf.name, status: "skipped", reason: "note access is not authorized" });
    continue;
  }
  const args = argsFor(leaf);
  if (!args) {
    results.push({
      command: leaf.name,
      status: "not_covered",
      reason: "no identifier for its required arguments could be harvested",
    });
    continue;
  }

  const outcome = run([...leaf.name.split(" "), ...args]);
  if (outcome.spawnFailed || outcome.parseFailed) {
    results.push({ command: leaf.name, status: "failed", reason: "no JSON envelope" });
    continue;
  }
  const envelope = outcome.envelope;
  if (!envelope.ok) {
    // "This article has no captions" and "the account is not entitled to this"
    // are answers, not faults -- reporting them honestly is the whole point of
    // the tool, so they must not fail its own smoke. Codes that mean the tool
    // or the service broke still do.
    const code = (envelope.error && envelope.error.code) || "";
    const answered = ["E_NOT_FOUND", "E_FORBIDDEN", "E_AUTH", "E_RATE_LIMITED"].includes(code);
    results.push({
      command: leaf.name,
      status: answered ? "answered" : "error",
      error_code: code,
    });
    continue;
  }

  // The check that only a live read can make: does the payload carry a field
  // the contract does not declare? A declared field that is absent is not a
  // defect on its own -- pagination and truncation fields appear only when the
  // page boundary is known -- so it is reported without failing the run.
  const declared = new Set((schemas[leaf.schema] || {}).fields || []);
  const actual = Object.keys(envelope.data || {}).filter((key) => key !== "_untrusted");
  const undeclared = actual.filter((key) => !declared.has(key));
  const absent = [...declared].filter((key) => !actual.includes(key));
  results.push({
    command: leaf.name,
    status: undeclared.length ? "undeclared_fields" : "match",
    schema: leaf.schema,
    undeclared_fields: undeclared,
    declared_but_absent: absent,
  });
}

// ---- report ----------------------------------------------------------------
//
// The report records command names, outcomes, and field names only. No ids, no
// payloads, no account content: it is evidence that the contract holds, and it
// has to be safe to keep alongside the code.

const counts = results.reduce((tally, entry) => {
  tally[entry.status] = (tally[entry.status] || 0) + 1;
  return tally;
}, {});
const failed = results.filter((entry) =>
  ["undeclared_fields", "failed", "error", "write_failed"].includes(entry.status)
);

const report = {
  tool: ref.tool,
  version: ref.version,
  ran_at: new Date().toISOString(),
  note_access_authorized: notesAuthorized,
  writes_included: includeWrites,
  counts,
  results,
};
fs.writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);

if (asJSON) {
  console.log(JSON.stringify({ counts, failures: failed.map((f) => f.command) }, null, 2));
} else {
  for (const entry of results) {
    const label = entry.command.padEnd(24);
    if (entry.status === "match") console.log(`  ok        ${label}`);
    else if (entry.status === "undeclared_fields")
      console.log(`  UNDECLARED ${label} ${entry.undeclared_fields.join(", ")}`);
    else if (entry.status === "answered") console.log(`  answered  ${label} ${entry.error_code}`);
    else if (entry.status === "write_ok") console.log(`  write ok  ${label}`);
    else if (entry.status === "write_failed") console.log(`  WRITE BAD ${label} ${entry.reason}`);
    else if (entry.status === "error") console.log(`  ERROR     ${label} ${entry.error_code}`);
    else if (entry.status === "failed") console.log(`  FAILED    ${label} ${entry.reason}`);
    else console.log(`  ${entry.status.padEnd(9)} ${label} ${entry.reason}`);
  }
  console.log();
  console.log(
    `live-smoke: ${counts.match || 0} matched, ${counts.undeclared_fields || 0} undeclared, ` +
      `${counts.answered || 0} answered-with-a-code, ${counts.error || 0} errored, ` +
      `${counts.not_covered || 0} not covered, ${counts.skipped || 0} skipped`
  );
  console.log(`report: ${reportPath}`);
}

process.exit(failed.length ? 1 : 0);
