"""Render go test -json output as a GitHub Actions job summary."""

import argparse
import collections
import html
import json
from pathlib import Path


def cell(value):
    return html.escape(str(value)).replace("|", "&#124;").replace("\n", " ")


def summarize(paths):
    runs = []
    for path in paths:
        tests = []
        packages = []
        with path.open() as source:
            for line in source:
                if not line.strip():
                    continue
                event = json.loads(line)
                if event.get("Action") not in {"pass", "fail", "skip"}:
                    continue
                if "Test" in event:
                    tests.append(event)
                else:
                    packages.append(event)
        status = "incomplete"
        if packages:
            status = "fail" if any(p["Action"] == "fail" for p in packages) else "pass"
        elapsed = sum(p.get("Elapsed", 0) for p in packages)
        runs.append((path.stem, status, elapsed, tests))

    lines = [
        "## E2E timings",
        "",
        "Tracing is enabled for every run. `default` uses Go's default test parallelism; "
        "`parallel12` uses `-parallel=12`. The trailing number identifies the repetition.",
        "",
        "| Run | Result | Suite seconds | Passed | Failed | Skipped |",
        "| --- | --- | ---: | ---: | ---: | ---: |",
    ]
    for name, status, elapsed, tests in runs:
        counts = collections.Counter(t["Action"] for t in tests)
        duration = f"{elapsed:.2f}" if status != "incomplete" else "unavailable"
        lines.append(
            f"| {cell(name)} | {status} | {duration} | "
            f"{counts['pass']} | {counts['fail']} | {counts['skip']} |"
        )
    lines.extend([
        "",
        "Counts include tests and subtests. Test durations include child tests and "
        "can overlap; they must not be added together. Suite seconds exclude compilation "
        "and runner setup. These repeated runs compare separate runners, so compare "
        "both repetitions before choosing a setting.",
    ])
    for name, _, _, tests in runs:
        lines.extend([
            "",
            f"### Slowest tests: {cell(name)}",
            "",
            "| Test | Result | Seconds |",
            "| --- | --- | ---: |",
        ])
        for event in sorted(tests, key=lambda t: t.get("Elapsed", 0), reverse=True)[:20]:
            lines.append(
                f"| {cell(event['Test'])} | {event['Action']} | {event.get('Elapsed', 0):.2f} |"
            )
        failures = [t for t in tests if t["Action"] == "fail"]
        if failures:
            lines.extend(["", "Failed tests: " + ", ".join(cell(t["Test"]) for t in failures)])
    return "\n".join(lines) + "\n"


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("results", type=Path, nargs="+", help="go test -json output files")
    print(summarize(parser.parse_args().results), end="")
