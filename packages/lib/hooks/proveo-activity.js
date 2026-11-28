// SPEC: _spec/packages/lib/agent-activity.puml
import { spawn } from "node:child_process"

const BUSY = new Set(["session.execution.started"])
const DONE = new Set(["session.execution.succeeded", "session.execution.failed", "session.execution.interrupted", "session.idle"])

export default {
  id: "proveo-activity",
  setup(ctx) {
    const controller = new AbortController()
    const busy = new Set()
    let last

    const mark = (state) => {
      if (state === last) return
      last = state
      try {
        const child = spawn("/usr/local/bin/proveo-entrypoint", ["activity", "opencode", state], { stdio: "ignore" })
        child.once("error", () => {})
      } catch {}
    }

    const events = (async () => {
      for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
        if (controller.signal.aborted) break
        const sessionID = event.data?.sessionID
        if (!sessionID) continue
        const status = event.type === "session.status" ? event.data.status?.type : undefined
        if (BUSY.has(event.type) || status === "busy" || status === "retry") busy.add(sessionID)
        else if (DONE.has(event.type) || status === "idle") busy.delete(sessionID)
        else continue
        mark(busy.size > 0 ? "busy" : "idle")
      }
    })().catch(() => {})

    return async () => {
      controller.abort()
      await events
    }
  },
}
