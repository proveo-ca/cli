module resource_queue {
  type Resources = { cpu: int, ram: int, disk: int }
  type Queue = { phases: str -> str, free: Resources, known: bool, reflected: Set[str], lightReady: bool }

  var queue: Queue

  pure val pool: Resources = { cpu: 5, ram: 12, disk: 20 }
  pure val zero: Resources = { cpu: 0, ram: 0, disk: 0 }
  pure val demands = Map(
    "heavy" -> { cpu: 3, ram: 8, disk: 12 },
    "light" -> { cpu: 2, ram: 4, disk: 8 }
  )
  pure val tasks = Set("heavy", "light")
  pure val working = Set("reserved", "running", "settling")

  pure def add(a: Resources, b: Resources): Resources = {
    cpu: a.cpu + b.cpu, ram: a.ram + b.ram, disk: a.disk + b.disk
  }
  pure def fits(a: Resources, b: Resources): bool =
    a.cpu <= b.cpu and a.ram <= b.ram and a.disk <= b.disk

  def held: Set[str] = tasks.filter(t => working.contains(queue.phases.get(t)))
  pure def reserved(ids: Set[str]): Resources = ids.fold(zero, (sum, t) => add(sum, demands.get(t)))
  def fitsNow(t: str): bool = all {
    queue.known,
    fits(add(reserved(held), demands.get(t)), pool),
    fits(add(reserved(held.exclude(queue.reflected)), demands.get(t)), queue.free),
  }
  def eligible(t: str): bool = all {
    queue.phases.get(t) == "queued",
    t != "light" or queue.lightReady,
    fitsNow(t),
  }

  pure val empty: Queue = {
    phases: Map("heavy" -> "queued", "light" -> "queued"),
    free: zero, known: false, reflected: Set(), lightReady: true,
  }
  action init = queue' = empty

  action sample(free, known) = queue' = {
    phases: queue.phases,
    free: free,
    known: known,
    reflected: held.filter(t => queue.phases.get(t) != "reserved"),
    lightReady: queue.lightReady,
  }

  action move(t, phase) = queue' = {
    phases: queue.phases.set(t, phase), free: queue.free,
    known: queue.known, reflected: queue.reflected, lightReady: queue.lightReady,
  }

  action setLightReady(ready) = all {
    queue.phases.get("light") == "queued",
    queue' = queue.with("lightReady", ready),
  }

  action admit(t) = all {
    eligible(t),
    t == "heavy" or not(eligible("heavy")),
    move(t, "reserved"),
  }
  action start(t) = all { queue.phases.get(t) == "reserved", move(t, "running") }
  action end(t) = all { queue.phases.get(t) == "running", move(t, "settling") }

  action release(t, phase) = queue' = {
    phases: queue.phases.set(t, phase),
    free: queue.free,
    known: queue.known and not(queue.reflected.contains(t)),
    reflected: queue.reflected.exclude(Set(t)),
    lightReady: queue.lightReady,
  }
  action publish(t) = all { queue.phases.get(t) == "settling", release(t, "attention") }
  action failedStart(t) = all { queue.phases.get(t) == "reserved", release(t, "attention") }
  action retry(t) = all { queue.phases.get(t) == "attention", move(t, "queued") }

  action step = any {
    {
      nondet t = tasks.oneOf()
      any { admit(t), start(t), end(t), publish(t), failedStart(t), retry(t) }
    },
    sample(pool, true),
    sample({ cpu: 1, ram: 12, disk: 20 }, true),
    sample({ cpu: 4, ram: 4, disk: 20 }, true),
    sample({ cpu: 4, ram: 12, disk: 4 }, true),
    sample(zero, false),
    setLightReady(false),
    setLightReady(true),
  }

  invariant withinPool = fits(reserved(held), pool)
  invariant reflectedLeases = queue.reflected.subseteq(held)
  invariant prerequisitesCleared = not(held.contains("light")) or queue.lightReady

  run waitingForRamTest = init
    .then(sample({ cpu: 4, ram: 4, disk: 20 }, true))
    .expect(not(eligible("heavy")) and eligible("light"))
    .then(admit("light"))
    .expect(queue.phases.get("light") == "reserved" and queue.phases.get("heavy") == "queued")

  run waitingForCpuTest = init
    .then(sample({ cpu: 1, ram: 12, disk: 20 }, true))
    .expect(not(eligible("heavy")) and not(eligible("light")))

  run waitingForDiskTest = init
    .then(sample({ cpu: 4, ram: 12, disk: 4 }, true))
    .expect(not(eligible("heavy")) and not(eligible("light")))

  run unknownSampleTest = init
    .then(sample(pool, false))
    .expect(not(eligible("heavy")) and not(eligible("light")))

  run releaseAfterCarryTest = init
    .then(sample(pool, true)).then(admit("heavy")).then(start("heavy"))
    .then(sample({ cpu: 1, ram: 4, disk: 8 }, true))
    .then(end("heavy"))
    .expect(not(eligible("light")))
    .then(publish("heavy"))
    .expect(not(eligible("light")))
    .then(sample(pool, true))
    .then(admit("light"))
    .expect(queue.phases.get("light") == "reserved")

  run pendingReservationTest = init
    .then(sample({ cpu: 3, ram: 12, disk: 20 }, true))
    .then(admit("heavy"))
    .expect(not(eligible("light")) and withinPool)

  run prerequisiteTest = init
    .then(sample({ cpu: 5, ram: 4, disk: 20 }, true))
    .then(setLightReady(false))
    .expect(not(eligible("light")))
    .then(setLightReady(true))
    .then(admit("light"))
    .expect(prerequisitesCleared and queue.phases.get("light") == "reserved")
}
