#### chase

YAML based command runner and `potentially` a (forward) build system. Chase primarily reads build specifications from a `chasefile` (or one of its many variants like, `Chasefile` or `ChaseFile`).

##### Features
- Easy to use
- Parallel commands
- Hopefully not slow
- Variables

```yaml
build:
    summary: "build..."
    command: gcc main.c main.h -o main

tests:
    summary: "build..."
    command: |

```