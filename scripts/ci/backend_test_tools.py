#!/usr/bin/env python3
"""Plan, summarise and measure the concurrent backend test groups.

The backend runner (`scripts/ci/test-backend.sh`) executes several `go test
-json` groups concurrently. This helper

* `plan`: splits the discovered PostgreSQL tests into balanced groups (known
  slow tests first, longest-processing-time-first), so no group is left running
  alone at the end;
* `report`: turns the group logs into one timing line per group, proves that
  every discovered PostgreSQL test reported a result and renders the original
  diagnostics of failed groups;
* `snapshot` / `resources`: record how much CPU and memory the job used.

It uses only the standard library, makes no network requests and never changes
a test verdict: the script's exit status still comes from `go test`. Stale or
missing weights only affect balance, never which tests run.
"""
import argparse
import json
import os
import sys
import time
from pathlib import Path

CGROUP_ROOT = Path("/sys/fs/cgroup")


def read_events(path):
    """Yield (event, raw_line) for every line; unparsable lines yield (None, line)."""
    with open(path, encoding="utf-8", errors="replace") as handle:
        for line in handle:
            try:
                yield json.loads(line), line
            except json.JSONDecodeError:
                yield None, line


def summarize_group(path):
    """Count top-level test outcomes. Subtests are folded into their parent."""
    counts = {"pass": 0, "fail": 0, "skip": 0}
    results = {}
    for event, _ in read_events(path):
        if not event:
            continue
        name = event.get("Test")
        action = event.get("Action")
        if name and "/" not in name and action in counts:
            counts[action] += 1
            results[name] = (action, float(event.get("Elapsed") or 0.0))
    return counts, results


def render_failure(path):
    """Rebuild plain `go test` diagnostics from the JSON Output events."""
    chunks = []
    for event, line in read_events(path):
        chunks.append(event.get("Output", "") if event else line)
    return "".join(chunks)


DEFAULT_WEIGHT = 0.8  # seconds; the measured mean of an unlisted test


def load_weights(path):
    """Read `seconds TestName` lines; comments and malformed lines are ignored."""
    weights = {}
    try:
        lines = Path(path).read_text(encoding="utf-8").splitlines()
    except OSError:
        return weights
    for line in lines:
        fields = line.split("#", 1)[0].split()
        if len(fields) == 2:
            try:
                weights[fields[1]] = float(fields[0])
            except ValueError:
                continue
    return weights


def plan_groups(names, groups, weights):
    """Assign every name to exactly one of `groups` buckets, heaviest first."""
    buckets = [[] for _ in range(groups)]
    loads = [0.0] * groups
    for name in sorted(names, key=lambda item: (-weights.get(item, DEFAULT_WEIGHT), item)):
        target = loads.index(min(loads))
        buckets[target].append(name)
        loads[target] += weights.get(name, DEFAULT_WEIGHT)
    return buckets


def missing_tests(expected, reported):
    return sorted(set(expected) - set(reported))


def format_group_line(name, status, counts, elapsed):
    total = sum(counts.values())
    return (
        f"=== {name}: exit {status}, {total} tests ({counts['pass']} passed, "
        f"{counts['fail']} failed, {counts['skip']} skipped) in {elapsed}s ==="
    )


# --- resource accounting -------------------------------------------------


def _read_int(path):
    try:
        return int(Path(path).read_text().split()[0])
    except (OSError, ValueError, IndexError):
        return None


def _read_keyed(path):
    values = {}
    try:
        for line in Path(path).read_text().splitlines():
            key, _, value = line.partition(" ")
            if value.strip().lstrip("-").isdigit():
                values[key] = int(value)
    except OSError:
        pass
    return values


def own_cgroup(root=CGROUP_ROOT, proc="/proc/self/cgroup"):
    try:
        for line in Path(proc).read_text().splitlines():
            hierarchy, _, rest = line.partition(":")
            if hierarchy == "0":
                return root / rest.partition(":")[2].lstrip("/")
    except OSError:
        pass
    return None


def limiting_cgroup(start, root=CGROUP_ROOT):
    """Nearest ancestor of `start` with a finite CPU quota, else None."""
    directory = start
    while root == directory or root in directory.parents:
        try:
            limit, period = (directory / "cpu.max").read_text().split()[:2]
        except (OSError, ValueError):
            limit = "max"
        if limit != "max":
            return directory, int(limit) / int(period)
        if directory == root:
            break
        directory = directory.parent
    return None


def snapshot(root=CGROUP_ROOT, proc="/proc/self/cgroup"):
    taken = {"time": time.time(), "cpu_count": os.cpu_count() or 0}
    start = own_cgroup(root, proc)
    if start and (start / "cpu.stat").exists():
        found = limiting_cgroup(start, root)
        scope, cpus = found if found else (start, None)
        stat = _read_keyed(scope / "cpu.stat")
        events = _read_keyed(scope / "memory.events")
        taken["cgroup"] = {
            "name": scope.name or "/",
            "cpu_quota_cpus": cpus,
            "usage_usec": stat.get("usage_usec"),
            "nr_throttled": stat.get("nr_throttled"),
            "throttled_usec": stat.get("throttled_usec"),
            "memory_current": _read_int(scope / "memory.current"),
            "memory_peak": _read_int(scope / "memory.peak"),
            "memory_max": _read_int(scope / "memory.max"),
            "memory_events_max": events.get("max"),
        }
    return taken


def describe_resources(before, after):
    """Return human-readable lines; empty when no cgroup data is available."""
    wall = max(after["time"] - before["time"], 0.001)
    start, end = before.get("cgroup"), after.get("cgroup")
    if not start or not end or start.get("usage_usec") is None:
        return [f"resources: no cgroup accounting available ({after['cpu_count']} logical CPUs visible)"]
    used = (end["usage_usec"] - start["usage_usec"]) / 1e6
    quota = end.get("cpu_quota_cpus")
    limit = f"limit {quota:.2f} CPUs" if quota else f"no CPU limit ({after['cpu_count']} visible)"
    lines = [
        f"resources: cgroup {end['name']}: {limit}; used {used:.0f} cpu-s in {wall:.0f}s "
        f"(average {used / wall:.2f} CPUs)"
    ]
    if end.get("nr_throttled") is not None and start.get("nr_throttled") is not None:
        throttled = end["nr_throttled"] - start["nr_throttled"]
        throttled_s = ((end["throttled_usec"] or 0) - (start["throttled_usec"] or 0)) / 1e6
        lines.append(f"resources: CPU throttled in {throttled} periods ({throttled_s:.1f} thread-s)")
    if end.get("memory_current") is not None:
        mebibytes = lambda value: "n/a" if value is None else f"{value / 1048576:.0f} MiB"
        memory_max = end.get("memory_max")
        ceiling = "unlimited" if not memory_max else mebibytes(memory_max)
        reclaim = ""
        if end.get("memory_events_max") is not None and start.get("memory_events_max") is not None:
            reclaim = f", memory limit reached {end['memory_events_max'] - start['memory_events_max']} times"
        lines.append(
            f"resources: memory now {mebibytes(end['memory_current'])}, "
            f"high-water {mebibytes(end.get('memory_peak'))}, limit {ceiling}{reclaim}"
        )
    return lines


# --- command line --------------------------------------------------------


def command_plan(args):
    names = [line.strip() for line in Path(args.names).read_text().splitlines() if line.strip()]
    for bucket in plan_groups(names, args.shards, load_weights(args.weights)):
        print("|".join(bucket))
    return 0


def command_snapshot(args):
    Path(args.output).write_text(json.dumps(snapshot()))
    return 0


def command_resources(args):
    before = json.loads(Path(args.before).read_text())
    for line in describe_resources(before, snapshot()):
        print(line)
    return 0


def command_report(args):
    log_dir = Path(args.log_dir)
    expected = [line.strip() for line in Path(args.expected).read_text().splitlines() if line.strip()] if args.expected else []
    reported = {}
    slowest = []
    failed = False
    total = 0
    for name in args.groups:
        status = (log_dir / f"{name}.status").read_text().strip() if (log_dir / f"{name}.status").exists() else "missing"
        elapsed = (log_dir / f"{name}.elapsed").read_text().strip() if (log_dir / f"{name}.elapsed").exists() else "0"
        log = log_dir / f"{name}.json"
        counts, results = summarize_group(log) if log.exists() else ({"pass": 0, "fail": 0, "skip": 0}, {})
        total += sum(counts.values())
        print(format_group_line(name, status, counts, elapsed))
        if name.startswith(args.expected_prefix):
            reported.update(results)
        for test, (_, seconds) in results.items():
            slowest.append((seconds, name, test))
        if status != "0":
            failed = True
            if log.exists():
                sys.stderr.write(render_failure(log))
            else:
                print(f"No log was captured for {name}", file=sys.stderr)
    for seconds, name, test in sorted(slowest, reverse=True)[: args.slowest]:
        print(f"slowest: {seconds:6.1f}s {test} ({name})")
    gaps = missing_tests(expected, reported) if expected else []
    if gaps:
        failed = True
        print(f"ERROR: {len(gaps)} discovered PostgreSQL tests reported no result: {', '.join(gaps[:10])}", file=sys.stderr)
    print(f"summary: {total} tests reported by {len(args.groups)} groups; {len(reported)} of {len(expected)} PostgreSQL tests accounted for")
    return 1 if failed else 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    plan = commands.add_parser("plan", help="print one pipe-joined test list per balanced group")
    plan.add_argument("--shards", type=int, required=True)
    plan.add_argument("--weights", required=True)
    plan.add_argument("names", help="file with one test name per line")
    plan.set_defaults(handler=command_plan)
    snap = commands.add_parser("snapshot", help="record cgroup/CPU counters")
    snap.add_argument("output")
    snap.set_defaults(handler=command_snapshot)
    res = commands.add_parser("resources", help="print usage since a snapshot")
    res.add_argument("before")
    res.set_defaults(handler=command_resources)
    rep = commands.add_parser("report", help="summarise group logs")
    rep.add_argument("--log-dir", required=True)
    rep.add_argument("--expected", help="file listing every PostgreSQL test that must report")
    rep.add_argument("--expected-prefix", default="postgres-")
    rep.add_argument("--slowest", type=int, default=5)
    rep.add_argument("groups", nargs="+")
    rep.set_defaults(handler=command_report)
    args = parser.parse_args(argv)
    return args.handler(args)


if __name__ == "__main__":
    sys.exit(main())
