#### chase

`Chase` is command runner and *potentially* a (forward) build system. Chase primarily reads build specifications from a `chasefile` placed in your outer directory consisting of one or more tasks.
<!-- (or one of its many variants like, `Chasefile` or `ChaseFile`) -->

<!-- #### Features
- Easy to use
- Parallel commands
- Variables
- Custom environments?
- Hopefully not slow -->

#### Using
```yaml
set shell = ["cmd", "/c"] 

set SOMEVAR = 02029a#some_special_key#29d

build:
    summary: "build main"
    cmds: gcc *.c *.h -o main

tests:
    uses: build
    summary: "run all tests"
    cmds: [ # arrays instead of pipes for multiple cmds
        "type {{ SOMEVAR }} > manifest.json",
        "./test --all"
    ]

hello:
    usage: [dir]
    cmds: echo {{ SOMEVAR }}
```

```bash
chase -l
> build    "build main"     gcc *.c *.h -o main
  tests    "run all tests"  *multiline
  hello    [dir]            echo {{ vars.SOMEVAR }}
```
```bash
chase #runs the build task
chase tests
```

chase expects the custom shell declaration at the top of the file. if it does not exist, commands will be run with any reasonable `sh` (Git bash if on windows)

see [./idea/readme.md](.idea/readme.md) for more