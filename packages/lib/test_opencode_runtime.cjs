// SPEC: _spec/defs/opencode/engine-local-history.puml, _spec/defs/opencode/native-v2-integration.puml

const assert = require("node:assert/strict")
const { execFile, spawn } = require("node:child_process")
const { once } = require("node:events")
const { mkdir, mkdtemp, readFile, readdir, rm, writeFile } = require("node:fs/promises")
const { tmpdir } = require("node:os")
const { dirname, join } = require("node:path")
const { test } = require("node:test")
const { promisify } = require("node:util")
const runtime = process.env.PROVEO_TEST_OPENCODE_RUNTIME || join(__dirname, "proveo-opencode-runtime")
const { nativeCommand, diagnostic } = require(runtime)

test("native command preserves argv around the separator", () => {
  for (const [args, expected] of [
    [["run", "hello"], ["run", "hello", "--auto", "--standalone"]],
    [["--log-level", "debug", "run", "--thinking", "hello"], ["--log-level", "debug", "run", "--thinking", "hello", "--auto", "--standalone"]],
    [["run", "--auto", "hello"], ["run", "--auto", "hello", "--standalone"]],
    [["run", "--", "hello world"], ["run", "--auto", "--standalone", "--", "hello world"]],
    [["run", "--server=http://127.0.0.1:4096", "hello"], ["run", "--server=http://127.0.0.1:4096", "hello", "--auto"]],
  ]) assert.deepEqual(nativeCommand("native", args), ["native", ...expected])
  for (const args of [[], ["--continue"], ["--prompt", "run"], ["/workspace/run"], ["mini"], ["session", "list"],
    ["models"], ["stats"], ["auth", "list"], ["api"], ["reload"], ["debug", "agents"], ["debug", "config"], ["serve"],
    ["service", "status"], ["acp"], ["mcp", "list"], ["plugin", "list"], ["upgrade"], ["pair"]]) {
    assert.equal(nativeCommand("native", args).includes("--auto"), false)
  }
})

test("diagnostic flags count only before the separator", () => {
  for (const args of [["--version"], ["-v"], ["run", "--help"], ["-h"], ["--completions", "bash"]]) assert.equal(diagnostic(args), true)
  for (const args of [["run", "--", "--help"], ["debug", "agents"], ["session", "list"], []]) assert.equal(diagnostic(args), false)
})

const nativeScript = `#!/usr/bin/env node
const fs = require("node:fs")
const path = require("node:path")
const env = process.env
const db = env.OPENCODE_DB || path.join(env.HOME, ".local/share/opencode/opencode.db")
fs.mkdirSync(path.dirname(db), { recursive: true })
fs.appendFileSync(db, "launch\\n")
const config = env.OPENCODE_CONFIG_DIR && Object.fromEntries(fs.readdirSync(env.OPENCODE_CONFIG_DIR).sort()
  .map(name => [name, fs.readFileSync(path.join(env.OPENCODE_CONFIG_DIR, name), "utf8")]))
fs.writeFileSync(env.FAKE_REPORT, JSON.stringify({ argv: process.argv.slice(2), pid: process.pid, db, config,
  temporary: env.TMPDIR, data: env.XDG_DATA_HOME, state: env.XDG_STATE_HOME }))
if (env.FAKE_SLEEP) {
  process.stdout.write("READY\\n")
  setTimeout(() => {}, 30000)
}
process.exitCode = Number(env.FAKE_EXIT || 0)
`

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), "opencode-runtime-test-"))
  t.after(() => rm(root, { recursive: true, force: true }))
  const home = join(root, "home"), dir = join(root, "tmp"), config = join(home, ".config/opencode")
  const db = join(home, ".local/share/opencode/opencode.db"), native = join(root, "native"), report = join(root, "report")
  for (const d of [dir, config, dirname(db)]) await mkdir(d, { recursive: true })
  await writeFile(db, "engine history\n")
  await writeFile(native, nativeScript, { mode: 0o755 })
  await writeFile(join(config, "opencode.json"), '{"model":"fixture/model"}')
  await writeFile(join(config, "service.json"), '{"env":{"OPENCODE_DB":"/elsewhere"}}')
  await writeFile(join(config, "service-extra.json"), "{}")
  const env = { PATH: process.env.PATH, HOME: home, TMPDIR: dir, FAKE_REPORT: report }
  return { root, dir, db, native, report, env }
}

test("diagnostics and the commit hook isolate the engine database", async t => {
  const f = await fixture(t)
  for (const [args, extra, code] of [
    ...[["--version"], ["-v"], ["--help"], ["run", "--help"], ["-h"], ["--completions", "bash"]].map(args => [args, {}, 0]),
    [["run", "--standalone", "-m", "fixture/model", "summarize"], { PROVEO_GIT_SYNC_MSG_INFLIGHT: "1" }, 0],
    [["--version"], { FAKE_EXIT: "7" }, 7],
  ]) {
    const run = promisify(execFile)(process.execPath, [runtime, f.native, ...args], {
      env: { ...f.env, ...extra }, cwd: f.root, timeout: 10000,
    })
    if (code === 0) await run
    else await assert.rejects(run, { code })
    const report = JSON.parse(await readFile(f.report, "utf8"))
    assert.deepEqual(report.argv, args)
    const scratch = dirname(report.data)
    assert.equal(dirname(scratch), f.dir)
    assert.equal(report.db, join(scratch, "data/opencode/opencode.db"))
    assert.equal(report.state, join(scratch, "state"))
    assert.equal(report.temporary, join(scratch, "tmp"))
    assert.deepEqual(report.config, { "opencode.json": '{"model":"fixture/model"}', "service.json": '{"disabled":true}' })
    assert.deepEqual(await readdir(f.dir), [])
    assert.equal(await readFile(f.db, "utf8"), "engine history\n")
  }
})

test("termination reaches a diagnostic child and removes its directory", { timeout: 10000 }, async t => {
  const f = await fixture(t)
  const processChild = spawn(process.execPath, [runtime, f.native, "--help"], {
    env: { ...f.env, FAKE_SLEEP: "1" }, cwd: f.root,
  })
  t.after(() => { if (processChild.exitCode === null) processChild.kill("SIGKILL") })
  const done = once(processChild, "exit")
  assert.match(String((await once(processChild.stdout, "data"))[0]), /READY/)
  processChild.kill("SIGTERM")
  assert.deepEqual(await done, [143, null])
  assert.deepEqual(await readdir(f.dir), [])
})

test("normal commands replace the wrapper and keep the engine database", { timeout: 10000 }, async t => {
  const f = await fixture(t)
  for (const args of [["run", "hello"], ["session", "list"], [], ["debug", "agents"]]) {
    const child = spawn(process.execPath, [runtime, f.native, ...args], { env: f.env, cwd: f.root })
    assert.deepEqual(await once(child, "exit"), [0, null])
    const report = JSON.parse(await readFile(f.report, "utf8"))
    assert.equal(report.pid, child.pid)
    assert.equal(report.db, f.db)
    assert.equal(report.temporary, f.dir)
    assert.deepEqual([f.native, ...report.argv], nativeCommand(f.native, args))
  }
  assert.equal(await readFile(f.db, "utf8"), "engine history\n" + "launch\n".repeat(4))
})
