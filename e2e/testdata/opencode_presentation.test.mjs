// SPEC: _spec/defs/opencode/native-v2-integration.puml, _spec/defs/opencode/engine-local-history.puml

import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { DatabaseSync } from "node:sqlite"
import { test } from "node:test"
import { fileURLToPath } from "node:url"

const native = process.env.PROVEO_TEST_OPENCODE_NATIVE
const driver = fileURLToPath(new URL("./opencode_presentation.mjs", import.meta.url))
const runtime = process.env.PROVEO_TEST_OPENCODE_RUNTIME || "/usr/local/bin/proveo-opencode-runtime"
const expected = JSON.parse(readFileSync(new URL("./opencode_presentation.json", import.meta.url)))
const dockerfile = readFileSync(new URL("../../defs/opencode/Dockerfile", import.meta.url), "utf8")
const [, preferences] = dockerfile.match(/^\s*OPENCODE_CLI_CONFIG_CONTENT='([^']+)'/m) ?? []
assert.deepEqual(JSON.parse(preferences), expected)

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), "opencode-presentation-"))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const home = join(root, "home"), config = join(home, ".config/opencode")
  mkdirSync(config, { recursive: true })
  const global = {
    session: { permissions: "prompt", markdown: "rendered", thinking: "hide", grouping: "auto", verbosity: "low", sidebar: "hide" },
    tabs: { mode: "off", indicators: "numbers" },
    mini: Object.fromEntries(Object.keys(expected.mini).map(key => [key, "hide"])),
  }
  const globalFile = join(config, "cli.json")
  writeFileSync(globalFile, JSON.stringify(global))
  writeFileSync(join(config, "service.json"), '{"disabled":true}')
  const env = { PATH: process.env.PATH, HOME: home, OPENCODE_DISABLE_MODELS_FETCH: "1",
    OPENCODE_DISABLE_AUTOUPDATE: "1", OPENCODE_CONFIG_PROJECT_DISABLE: "1",
    XDG_CACHE_HOME: "/home/agent/.cache", TERM: "xterm-256color", OPENCODE_CLI_CONFIG_CONTENT: preferences }
  function drive(mode, value) {
    const report = JSON.parse(execFileSync(process.execPath, [driver, mode, value, process.execPath, runtime, native, root],
      { env, timeout: 45000, encoding: "utf8" }))
    assert.deepEqual(JSON.parse(readFileSync(globalFile)), global)
    return report
  }
  return { root, home, env, drive }
}

test("global preferences apply without inline preferences", { skip: !native }, t => {
  const f = fixture(t)
  f.env.OPENCODE_CLI_CONFIG_CONTENT = ""
  f.drive("presentation", "global")
})

test("inline preferences override global preferences", { skip: !native }, t => fixture(t).drive("presentation", "inline"))

test("invalid mini preferences fall back to the global configuration", { skip: !native }, t => {
  for (const key of Object.keys(expected.mini)) {
    const f = fixture(t)
    const invalid = JSON.parse(preferences)
    invalid.mini[key] = "invalid-contract-value"
    f.env.OPENCODE_CLI_CONFIG_CONTENT = JSON.stringify(invalid)
    f.drive("presentation", "global")
  }
})

test("headless run approves a shell ask and persists its result", { skip: !native }, t => {
  const f = fixture(t), report = f.drive("permission", "ask")
  assert.equal(report.shellOutput, "offline shell permission fixture\n")
  assert.equal(readFileSync(join(f.root, "shell-executed"), "utf8"), report.shellOutput)
  const db = new DatabaseSync(join(f.home, ".local/share/opencode/opencode.db"))
  try {
    const parts = db.prepare("SELECT data FROM session_message").all().flatMap(row => JSON.parse(row.data).content || [])
    const shell = parts.filter(part => part.type === "tool" && part.name === "shell")
    assert.equal(shell.length, 1)
    assert.equal(shell[0].state.status, "completed")
    assert.equal(shell[0].state.metadata.exit, 0)
    assert.deepEqual(shell[0].state.content, [{ type: "text", text: report.shellOutput }])
  } finally {
    db.close()
  }
})

for (const policy of ["deny", "prompt"]) {
  test(`shell policy ${policy} refuses the fixture`, { skip: !native }, t => {
    const f = fixture(t)
    f.drive("permission", policy)
    assert.equal(existsSync(join(f.root, "shell-executed")), false)
  })
}

test("native diagnostics remain compatible", { skip: !native }, t => {
  const f = fixture(t)
  for (const args of [["--version"], ["--help"], ["run", "--help"], ["debug", "agents"], ["session", "list", "--format", "json"]]) {
    const out = execFileSync(process.execPath, [runtime, native, ...args], { env: f.env, cwd: f.root, timeout: 20000, encoding: "utf8" })
    if (args[0] === "--version") {
      const version = process.env.PROVEO_TEST_OPENCODE_VERSION
      if (version) assert.equal(out.trim(), "opencode v" + version)
      else assert.match(out.trim(), /^opencode v2\.\d+\.\d+$/)
    }
    if (args[0] === "run") {
      assert.match(out, /--auto/)
      assert.match(out, /not explicitly denied/)
    }
  }
})
