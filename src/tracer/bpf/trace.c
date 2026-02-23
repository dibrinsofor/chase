//go:build ignore

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_core_read.h>

#define MAX_PATH_LEN 256
#define MAX_ARGV_LEN 256
#define MAX_ENTRIES 10240

#define OP_READ  0
#define OP_WRITE 1
#define OP_OPEN  2

// Event types for ring buffer
#define EVENT_FILE 0
#define EVENT_EXEC 1

struct event {
    u32 pid;
    u32 tid;
    u64 timestamp;
    u32 flags;
    u8  op;
    u8  event_type;
    u8  _pad[2];
    char path[MAX_PATH_LEN];
};

// Exec event captures subprocess spawning
struct exec_event {
    u32 pid;
    u32 ppid;
    u64 timestamp;
    u8  event_type;
    u8  _pad[3];
    char comm[16];
    char filename[MAX_PATH_LEN];
    char argv[MAX_ARGV_LEN];
};

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, MAX_ENTRIES * sizeof(struct event));
} events SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, MAX_ENTRIES * sizeof(struct exec_event));
} exec_events SEC(".maps");

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
    e->event_type = EVENT_FILE;

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

SEC("tracepoint/syscalls/sys_enter_execve")
int trace_execve(struct trace_event_raw_sys_enter *ctx) {
    u64 pid_tgid = bpf_get_current_pid_tgid();
    u32 pid = pid_tgid >> 32;

    if (!should_trace(pid)) {
        return 0;
    }

    struct exec_event *e = bpf_ringbuf_reserve(&exec_events, sizeof(*e), 0);
    if (!e) {
        return 0;
    }

    e->pid = pid;
    e->timestamp = bpf_ktime_get_ns();
    e->event_type = EVENT_EXEC;

    // Get parent PID
    struct task_struct *task = (struct task_struct *)bpf_get_current_task();
    e->ppid = BPF_CORE_READ(task, real_parent, tgid);

    // Get comm (process name)
    bpf_get_current_comm(e->comm, sizeof(e->comm));

    // Get filename (first argument to execve)
    const char *filename = (const char *)ctx->args[0];
    bpf_probe_read_user_str(e->filename, sizeof(e->filename), filename);

    // Get first argv entry for command identification
    const char *const *argv = (const char *const *)ctx->args[1];
    if (argv) {
        const char *arg0;
        bpf_probe_read_user(&arg0, sizeof(arg0), argv);
        if (arg0) {
            bpf_probe_read_user_str(e->argv, sizeof(e->argv), arg0);
        }
    }

    bpf_ringbuf_submit(e, 0);
    return 0;
}

char LICENSE[] SEC("license") = "GPL";
