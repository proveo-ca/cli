module resource_queue {
  type Queue = { phases: str -> str, freeDisk: int, known: bool, reflected: Set[str], secondReady: bool }

  var queue: Queue

  pure val diskPool = 64
  pure val diskPerSandbox = 32
  pure val tasks = Set("first", "second")
  pure val working = Set("reserved", "running", "settling")

  def held: Set[str] = tasks.filter(t => working.contains(queue.phases.get(t)))
  pure def reserved(ids: Set[str]): int = ids.size() * diskPerSandbox
  def fitsNow: bool = all {
    queue.known,
    reserved(held) + diskPerSandbox <= diskPool,
    reserved(held.exclude(queue.reflected)) + diskPerSandbox <= queue.freeDisk,
  }
  def eligible(t: str): bool = all {
    queue.phases.get(t) == "queued",
    t != "second" or queue.secondReady,
    fitsNow,
  }

  pure val empty: Queue = {
    phases: Map("first" -> "queued", "second" -> "queued"),
    freeDisk: 0, known: false, reflected: Set(), secondReady: true,
  }
  action init = queue' = empty

  action sample(freeDisk, known) = queue' = {
    phases: queue.phases,
    freeDisk: freeDisk,
    known: known,
    reflected: if (known) held.filter(t => queue.phases.get(t) != "reserved") else Set(),
    secondReady: queue.secondReady,
  }

  action move(t, phase) = queue' = {
    phases: queue.phases.set(t, phase), freeDisk: queue.freeDisk,
    known: queue.known, reflected: queue.reflected, secondReady: queue.secondReady,
  }

  action setSecondReady(ready) = all {
    queue.phases.get("second") == "queued",
    queue' = queue.with("secondReady", ready),
  }

  action admit(t) = all {
    eligible(t),
    t == "first" or not(eligible("first")),
    move(t, "reserved"),
  }
  action start(t) = all { queue.phases.get(t) == "reserved", move(t, "running") }
  action end(t) = all { queue.phases.get(t) == "running", move(t, "settling") }

  action release(t, phase) = queue' = {
    phases: queue.phases.set(t, phase),
    freeDisk: queue.freeDisk,
    known: queue.known and not(queue.reflected.contains(t)),
    reflected: queue.reflected.exclude(Set(t)),
    secondReady: queue.secondReady,
  }
  action publish(t) = all { queue.phases.get(t) == "settling", release(t, "attention") }
  action failedStart(t) = all { queue.phases.get(t) == "reserved", release(t, "attention") }
  action retry(t) = all { queue.phases.get(t) == "attention", move(t, "queued") }

  action step = any {
    {
      nondet t = tasks.oneOf()
      any { admit(t), start(t), end(t), publish(t), failedStart(t), retry(t) }
    },
    sample(diskPool, true),
    sample(48, true),
    sample(32, true),
    sample(16, true),
    sample(0, false),
    setSecondReady(false),
    setSecondReady(true),
  }

  invariant withinPool = reserved(held) <= diskPool
  invariant reflectedLeases = queue.reflected.subseteq(held)
  invariant prerequisitesCleared = not(held.contains("second")) or queue.secondReady

  run exactCapacityTest = init
    .then(sample(diskPool, true))
    .then(admit("first")).then(admit("second"))
    .expect(reserved(held) == diskPool and withinPool)

  run waitingForDiskTest = init
    .then(sample(16, true))
    .expect(not(eligible("first")) and not(eligible("second")))

  run unknownSampleTest = init
    .then(sample(diskPool, false))
    .expect(not(eligible("first")) and not(eligible("second")))

  run releaseAfterCarryTest = init
    .then(sample(diskPool, true)).then(admit("first")).then(start("first"))
    .then(sample(16, true)).then(end("first"))
    .expect(not(eligible("second")))
    .then(publish("first"))
    .expect(not(queue.known) and not(eligible("second")))
    .then(sample(32, true)).then(admit("second"))
    .expect(queue.phases.get("second") == "reserved")

  run pendingReservationTest = init
    .then(sample(48, true)).then(admit("first"))
    .expect(not(eligible("second")) and withinPool)

  run reflectedReservationTest = init
    .then(sample(diskPool, true)).then(admit("first")).then(start("first"))
    .then(sample(32, true)).then(admit("second"))
    .expect(reserved(held) == diskPool and reflectedLeases)

  run prerequisiteTest = init
    .then(sample(diskPool, true)).then(admit("first"))
    .then(setSecondReady(false))
    .expect(not(eligible("second")))
    .then(setSecondReady(true)).then(admit("second"))
    .expect(prerequisitesCleared and queue.phases.get("second") == "reserved")
}
