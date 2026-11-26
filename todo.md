# Chase Build System - Bug Fixes

## Critical Issues

- [ ] **fsatrace LLDB backend not working on macOS** - LLDB Python module not found, tracing completely non-functional
- [ ] **Silent failure when tracing fails** - No warning to user that incremental builds are disabled; `ParseOutput()` error ignored at `src/exec/executor.go:294`

## Potential Memory/Crash Issues

- [ ] **Recursive stack overflow in fsatrace `uniq()`** - `fsatrace/src/fsatrace.c:85-98` uses unbounded recursion; could crash with many file accesses
- [ ] **Buffer overflow risk in fsatrace** - `strcpy(d, s)` at `fsatrace/src/fsatrace.c:90` has no bounds check

## Minor Issues

- [ ] **Cache directory created but empty** - Directories `.chase/targets/` and `.chase/commands/` created even when no data to store
