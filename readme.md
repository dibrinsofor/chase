### chase

`Chase` is command runner and a (forward) build system. It primarily reads build
instructions from a `chasefile` placed in your project's directory consisting of
one or more tasks/dashes and then is a able to infer an incremental build
sequence by tracing all of the command's dependencies.

For a trivial C program with source and header files, developers must either
rebuild the entire program whenever a file changes, or adopt a build system like
Make or Bazel for incremental builds. With Make, this might look like:

```Make
program: main.o x.o y.o
  gcc -o program main.o x.o y.o
main.o: main.c x.h y.h
  gcc -c -o main.o main.c
x.o: x.c
  gcc -c -o x.o x.c
y.o: y.c y.h
  gcc -c -o y.o y.c
```

This requires manually specifying every dependency and ensuring each step's
inputs are _complete_. For example, if the developer omits a header file, you
get stale builds. Chase does not need explicit dependencies or targets. You
write the script that builds your code; and, Chase traces file accesses to infer
the dependency graph and repeats only the necessary steps when inputs change. So
an equivalent chasefile will look like this:

```Chasefile
build:
    cmds: "gcc -o program main.c x.c y.c"
```

#### Building

Chase traces file access by hooking filesystem calls. By default, it uses
[fsatrace](https://github.com/dibrinsofor/fsatrace), which interposes via
`LD_PRELOAD` on Linux, `DYLD_INTERPOSE` on macOS, and DLL injection on Windows.
On Linux, an experimental eBPF backend can hook at the kernel level instead,
enabling finer-grained subprocess tracing.

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

| Platform | Tracing Mechanism | Notes                                  |
| -------- | ----------------- | -------------------------------------- |
| Linux    | LD_PRELOAD        | Works with dynamically linked binaries |
| macOS    | DYLD_INTERPOSE    | Use non-SIP shells                     |
| Windows  | DLL injection     | Needs both 32-bit and 64-bit DLLs      |

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

Chase expects a shell declaration at the top of the file. If not provided,
commands run with `bash` on Unix or `cmd` on Windows.

<!-- see [./idea/readme.md](.idea/readme.md) for more -->

#### Performance

<!-- Add actual perf benchmark -->

Chase's key overhead comes from file access tracing. Here's how the available
tracing approaches compare to `fsatrace`:

| Approach | Overhead | Notes                          |
| -------- | -------- | ------------------------------ |
| eBPF     | ~1-5%    | Linux only, requires CAP_BPF   |
| fsatrace | ~2-10%   | Cross-platform (Chase default) |
| ptrace   | 10-50x   | Context switch per syscall     |
| strace   | 50-173x  | Debugging only                 |

Estimates derived from
[Brendan Gregg's eBPF benchmarks](https://www.brendangregg.com/ebpf.html) and
[PingCAP's syscall tracing analysis](https://www.pingcap.com/blog/how-to-trace-linux-system-calls-in-production-with-minimal-impact-on-performance/).
See [.idea/tracing-sandbox-comparison.md](.idea/tracing-sandbox-comparison.md)
for details.

#### Examples

See the [examples/](examples/) directory for Chasefile translations of popular
projects:

- **Redis** - Multi-binary build with dependencies
- **jq** - Parser generation with bison/flex
- **SQLite** - Amalgamation build pattern
- **curl** - Complex optional dependencies
