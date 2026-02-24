#### chase

`Chase` is command runner and *potentially* a (forward) build system. Chase primarily reads build specifications from a `chasefile` placed in your outer directory consisting of one or more tasks.
<!-- (or one of its many variants like, `Chasefile` or `ChaseFile`) -->


#### Building

Chase uses [fsatrace](https://github.com/jacereda/fsatrace) for cross-platform file access tracing.

```bash
# Clone with submodules
git clone --recurse-submodules https://github.com/dibrinsofor/chase
cd chase

# Build fsatrace (required for tracing)
cd fsatrace && make && cd ..

# Build chase
go build .
```

**Platform Notes:**
| Platform | Tracing Mechanism | Notes |
|----------|-------------------|-------|
| Linux | LD_PRELOAD | Works with dynamically linked binaries |
| macOS | DYLD_INSERT_LIBRARIES | Requires SIP disabled on 10.11+ |
| Windows | DLL injection | Needs both 32-bit and 64-bit DLLs |

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

chase expects the custom shell declaration at the top of the file. if it does not exist, commands will be run with any reasonable `sh` (Git bash if on windows)

see [./idea/readme.md](.idea/readme.md) for more

#### todo
- [ ] Run Parallel commands
- [ ] Improve UI
