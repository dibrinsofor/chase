//go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

#define MAX_PATH_LEN 256
#define MAX_ENTRIES 10240

#define OP_READ  0
#define OP_WRITE 1
#define OP_OPEN  2

struct event {
    u32 pid;
    u32 tid;
    u64 timestamp;
    u32 flags;
    u8  op;
    char path[MAX_PATH_LEN];
};

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, MAX_ENTRIES * sizeof(struct event));
} events SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, MAX_ENTRIES);
    __type(key, u32);
    __type(value, u32);
} target_pids SEC(".maps");

const volatile u32 filter_pid = 0;
const volatile bool follow_children = true;

static __always_inline bool should_trace(u32 pid) {
    if (filter_pid == 0) {
        return true;
    }
    if (pid == filter_pid) {
        return true;
    }
    if (follow_children) {
        u32 *found = bpf_map_lookup_elem(&target_pids, &pid);
        if (found) {
            return true;
        }
    }
    return false;
}

SEC("tracepoint/syscalls/sys_enter_openat")
int trace_openat_enter(struct trace_event_raw_sys_enter *ctx) {
    u64 pid_tgid = bpf_get_current_pid_tgid();
    u32 pid = pid_tgid >> 32;
    u32 tid = (u32)pid_tgid;

    if (!should_trace(pid)) {
        return 0;
    }

    struct event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
    if (!e) {
        return 0;
    }

    e->pid = pid;
    e->tid = tid;
    e->timestamp = bpf_ktime_get_ns();
    e->flags = (u32)ctx->args[2];
    e->op = OP_OPEN;

    const char *pathname = (const char *)ctx->args[1];
    bpf_probe_read_user_str(e->path, sizeof(e->path), pathname);

    bpf_ringbuf_submit(e, 0);
    return 0;
}

SEC("tracepoint/sched/sched_process_fork")
int trace_fork(struct trace_event_raw_sched_process_fork *ctx) {
    if (!follow_children) {
        return 0;
    }

    u32 parent_pid = ctx->parent_pid;
    u32 child_pid = ctx->child_pid;

    if (!should_trace(parent_pid)) {
        return 0;
    }

    u32 val = 1;
    bpf_map_update_elem(&target_pids, &child_pid, &val, BPF_ANY);
    return 0;
}

SEC("tracepoint/sched/sched_process_exit")
int trace_exit(struct trace_event_raw_sched_process_template *ctx) {
    u32 pid = ctx->pid;
    bpf_map_delete_elem(&target_pids, &pid);
    return 0;
}

char LICENSE[] SEC("license") = "GPL";
