# Chase Examples: Makefile Translations

This directory contains Chasefile translations of real-world Makefiles from popular open-source projects. Each example demonstrates how common Makefile patterns translate to Chase's syntax.

## Examples

| Project | Description | Complexity |
|---------|-------------|------------|
| [redis/](redis/) | In-memory data store | Medium - multi-binary build with deps |
| [jq/](jq/) | JSON processor | Medium - generated parser, autotools |
| [sqlite/](sqlite/) | Embedded database | High - amalgamation build, code gen |
| [curl/](curl/) | Transfer tool | High - many optional dependencies |

## Translation Guide

### Variables

```makefile
# Makefile
CC = gcc
CFLAGS = -O2 -Wall
```

```chasefile
# Chasefile
set CC = "gcc"
set CFLAGS = "-O2 -Wall"
```

### Targets and Dependencies

```makefile
# Makefile
build: deps
	$(CC) -o app main.c

deps:
	make -C libs
```

```chasefile
# Chasefile
build:
    uses: deps
    cmds: "$CC -o app main.c"

deps:
    cmds: "make -C libs"
```

### Automatic Variables

```makefile
# Makefile
%.o: %.c
	$(CC) -c $< -o $@
```

```chasefile
# Chasefile (explicit)
compile:
    inputs: ["src/main.c", "src/util.c"]
    outputs: ["build/main.o", "build/util.o"]
    cmds: "$CC -c {inputs} -o {outputs}"
```

### Phony Targets

```makefile
# Makefile
.PHONY: clean test
clean:
	rm -f *.o
```

```chasefile
# Chasefile (all tasks are inherently phony)
clean:
    cmds: "rm -f *.o"
```

### Conditional Logic

```makefile
# Makefile
ifeq ($(DEBUG),1)
  CFLAGS += -g
endif
```

```chasefile
# Chasefile (handle at shell level or separate tasks)
build-debug:
    summary: "Build with debug symbols"
    cmds: "$CC -g -O0 -o app main.c"

build-release:
    summary: "Build optimized"
    cmds: "$CC -O2 -o app main.c"
```

## Key Differences

| Feature | Make | Chase |
|---------|------|-------|
| Dependency tracing | Manual declaration | Automatic via fsatrace |
| Pattern rules | `%.o: %.c` | Explicit inputs/outputs |
| Parallel execution | `make -j4` | `chase -j 4` |
| Variables | `$(VAR)` | `$VAR` or `{var}` |
| Default target | First target | First task (or `build`) |
| Phony targets | Requires `.PHONY` | All tasks are phony |
| File prerequisites | Part of target | `inputs:` field |
| File products | Implicit | `outputs:` field |

## Running Examples

These examples are illustrative—they reference source files from the original projects. To run them:

1. Clone the target project
2. Copy the Chasefile into the project root
3. Run `chase -l` to list available tasks
4. Run `chase <taskname>` to execute

```bash
git clone https://github.com/redis/redis
cp examples/redis/Chasefile redis/
cd redis
chase -l
chase redis-server
```

## Why Chase?

Chase's automatic dependency tracing means you spend less time maintaining build files:

- **No forgotten dependencies**: Chase traces what files commands actually read
- **No stale builds**: Accurate tracking means correct incremental builds
- **Simpler syntax**: Focus on what to build, not dependency bookkeeping
- **Lint checking**: `chase -lint` compares declared vs. actual dependencies
