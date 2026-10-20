#### chase

`Chase` is command runner and a (forward) build system. It primarily reads build
instructions from a `chasefile` placed in your outer directory consisting of one
or more tasks/chases and then traces all of its dependencies.
 <!-- It builds a DAG -->
<!-- (or one of its many variants like, `Chasefile` or `ChaseFile`) -->

#### Building

<!-- mention other rfeatures, paralllesim and monadic engine -->

Chase uses [fsatrace](https://github.com/dibrinsofor/fsatrace) for
cross-platform file access tracing. We also support an experimental flag that
attmepts tracing with ebpf on Linux and Windows.

```bash
# Clone with submodules
git clone --recurse-submodules https://github.com/dibrinsofor/chase
cd chase

# Build fsatrace
cd fsatrace && make && cd ..

# Build chase
go build .
```

**Platform Notes:**

| Platform | Tracing Mechanism     | Notes                                  |
| -------- | --------------------- | -------------------------------------- |
| Linux    | LD_PRELOAD            | Works with dynamically linked binaries |
| macOS    | DYLD_INSERT_LIBRARIES | Requires SIP disabled on 10.11+        |
| Windows  | DLL injection         | Needs both 32-bit and 64-bit DLLs      |

**Experimental eBPF (Linux only):**

```bash
# Build with eBPF support for more detailed subprocess tracing
go build -tags experimental_ebpf .
```

#### Using

see [sample chasefile](chasefile)

```bash
chase -l
  build    summary: "build main"
  tests    summary: "run all tests"
  hello    --
```

```bash
chase #runs the build task
chase tests # runs only the test dash
```

chase expects the custom shell declaration at the top of the file. if it does
not exist, commands will be run with any reasonable `sh` (Git bash if on
windows)

see [./idea/readme.md](.idea/readme.md) for more

#### Performance

Chase's key overhead comes from file access tracing. Here's how the available
tracing approaches compare (from [benchmarks](benchmarks/)):

```
Tracing Overhead (lower = better)
─────────────────────────────────────────────────────────────────────
eBPF          ▓░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  ~1-5%
fsatrace      ▓▓░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  ~2-10%
watcher       ▓▓▓░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  ~5-15%
ptrace        ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░░░░░░░░░░░  10-50x
strace        ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓  50-173x
─────────────────────────────────────────────────────────────────────
```

Chase uses **fsatrace** by default (cross-platform, low overhead). For Linux
users needing subprocess-level tracing, **eBPF** is available via
`-tags experimental_ebpf`.

See [.idea/tracing-sandbox-comparison.md](.idea/tracing-sandbox-comparison.md)
for detailed methodology and sources.

#### Examples

See the [examples/](examples/) directory for Chasefile translations of popular
projects:

- **Redis** - Multi-binary build with dependencies
- **jq** - Parser generation with bison/flex
- **SQLite** - Amalgamation build pattern
- **curl** - Complex optional dependencies

#### todo

- [ ] Improve UI
